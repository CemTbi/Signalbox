// SPDX-License-Identifier: Apache-2.0

package apis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
	"github.com/CemTbi/Signalbox/pkg/specengine"
)

// Spec is one OpenAPI document of a service. Its content lives in its
// revisions.
type Spec struct {
	ID                uuid.UUID
	ServiceID         uuid.UUID
	Name              string
	CurrentRevisionID uuid.UUID
	// Version increments on every change, a new revision included; the API
	// sends it as the ETag.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Revision is one saved version of a spec's document. Revisions never
// change.
type Revision struct {
	ID       uuid.UUID
	SpecID   uuid.UUID
	Number   int
	Message  string
	FileType string // yaml or json
	// Format and OpenAPIVersion are what the document declares, such as
	// openapi-3.1 and 3.1.1.
	Format         specengine.Format
	OpenAPIVersion string
	// Title and APIVersion come from the document's info object.
	Title      string
	APIVersion string
	SizeBytes  int
	SHA256     [32]byte
	Counts     specengine.Counts
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
}

// bom is the UTF-8 byte order mark some editors put before a document.
const bom = "\xef\xbb\xbf"

// MaxSpecBytes is the largest document a revision holds.
const MaxSpecBytes = 10 << 20

const maxMessage = 500

// checked is a document that passed the checks a revision needs.
type checked struct {
	content []byte
	report  specengine.Report
	sha     [32]byte
}

// check reads and checks a document. It runs before any transaction, so a
// slow check holds no locks.
func (m *Module) check(ctx context.Context, content string, errs *validate.Errors) (*checked, error) {
	switch {
	case strings.TrimSpace(content) == "":
		errs.Add("/content", "minLength", "Paste or upload the OpenAPI document.")
		return nil, nil
	case len(content) > MaxSpecBytes:
		errs.Add("/content", "maxLength", fmt.Sprintf("The document is larger than %d MiB.", MaxSpecBytes>>20))
		return nil, nil
	case !utf8.ValidString(content):
		errs.Add("/content", "format", "The document isn't valid UTF-8 text.")
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, m.checkTimeout)
	defer cancel()
	data := []byte(content)
	report, err := specengine.Check(ctx, data, specengine.Options{SourceName: "spec"}, specengine.RuleSetRecommended)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrCheckTimeout
	}
	if err != nil {
		return nil, fmt.Errorf("check the document: %w", err)
	}
	if report.Format == "" {
		return nil, &UnreadableError{Report: report}
	}
	return &checked{content: data, report: report, sha: sha256.Sum256(data)}, nil
}

func message(errs *validate.Errors, v string) string {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxMessage {
		errs.Add("/message", "maxLength", fmt.Sprintf("Use at most %d characters.", maxMessage))
	}
	return v
}

// insertRevision stores a checked document as the given revision number.
func insertRevision(ctx context.Context, tx pgx.Tx, specID uuid.UUID, number int, msg string, c *checked, userID uuid.UUID) (Revision, error) {
	fileType := "yaml"
	if trimmed := strings.TrimLeft(string(c.content), " \t\r\n"+bom); strings.HasPrefix(trimmed, "{") {
		fileType = "json"
	}
	info := specengine.Info{}
	if c.report.Info != nil {
		info = *c.report.Info
	}
	report, err := json.Marshal(c.report)
	if err != nil {
		return Revision{}, err
	}
	r := Revision{
		ID: id.New(), SpecID: specID, Number: number, Message: msg, FileType: fileType,
		Format: c.report.Format, OpenAPIVersion: c.report.Version, Title: info.Title, APIVersion: info.Version,
		SizeBytes: len(c.content), SHA256: c.sha, Counts: c.report.Counts, CreatedBy: userID,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO spec_revisions (id, spec_id, number, message, content, file_type, format, openapi_version, title,
			api_version, size_bytes, sha256, errors, warnings, infos, hints, report, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING created_at`,
		r.ID, r.SpecID, r.Number, r.Message, string(c.content), r.FileType, string(r.Format), r.OpenAPIVersion, r.Title,
		r.APIVersion, r.SizeBytes, r.SHA256[:], r.Counts.Errors, r.Counts.Warnings, r.Counts.Infos, r.Counts.Hints,
		report, userID,
	).Scan(&r.CreatedAt)
	return r, err
}

const specColumns = `id, service_id, name, coalesce(current_revision_id, '00000000-0000-0000-0000-000000000000'), version, created_at, updated_at`

func readSpec(row pgx.Row) (Spec, error) {
	var s Spec
	err := row.Scan(&s.ID, &s.ServiceID, &s.Name, &s.CurrentRevisionID, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// ListSpecs lists a service's specs, oldest first.
func (m *Module) ListSpecs(ctx context.Context, userID, serviceID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[Spec], error) {
	if err := m.authorizeFor(ctx, userID, projectOfService, serviceID, read); err != nil {
		return workspaces.Page[Spec]{}, err
	}
	limit := limitOf(p)
	rows, err := m.db.Query(ctx, `
		SELECT `+specColumns+` FROM specs
		WHERE service_id = $1 AND ($2::uuid IS NULL OR id > $2)
		ORDER BY id LIMIT $3`, serviceID, afterArg(p.After), limit+1)
	if err != nil {
		return workspaces.Page[Spec]{}, fmt.Errorf("list specs: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Spec, error) { return readSpec(row) })
	if err != nil {
		return workspaces.Page[Spec]{}, fmt.Errorf("list specs: %w", err)
	}
	return page(items, limit), nil
}

// CreateSpec adds a spec to a service, with content as its first revision.
// Owners, admins and editors may.
func (m *Module) CreateSpec(ctx context.Context, userID, serviceID uuid.UUID, name, content string) (Spec, Revision, error) {
	var errs validate.Errors
	name = validate.Name(&errs, "/name", name)
	doc, err := m.check(ctx, content, &errs)
	if err != nil {
		return Spec{}, Revision{}, err
	}
	if err := errs.Err(); err != nil {
		return Spec{}, Revision{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfService, serviceID, edit); err != nil {
		return Spec{}, Revision{}, err
	}
	s := Spec{ID: id.New(), ServiceID: serviceID, Name: name}
	var rev Revision
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO specs (id, service_id, name, next_revision) VALUES ($1, $2, $3, 2)`, s.ID, serviceID, name); err != nil {
			return err
		}
		var err error
		if rev, err = insertRevision(ctx, tx, s.ID, 1, "", doc, userID); err != nil {
			return err
		}
		s, err = readSpec(tx.QueryRow(ctx, `UPDATE specs SET current_revision_id = $2 WHERE id = $1 RETURNING `+specColumns, s.ID, rev.ID))
		return err
	})
	switch {
	case postgres.IsForeignKeyViolation(err):
		return Spec{}, Revision{}, ErrNotFound // the service was deleted meanwhile
	case err != nil:
		return Spec{}, Revision{}, fmt.Errorf("create spec: %w", err)
	}
	m.logger.InfoContext(ctx, "created spec", id.LogAttr("spec_id", id.Spec, s.ID),
		id.LogAttr("service_id", id.Service, serviceID), id.LogAttr("user_id", id.User, userID))
	return s, rev, nil
}

// GetSpec returns a spec.
func (m *Module) GetSpec(ctx context.Context, userID, specID uuid.UUID) (Spec, error) {
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, read); err != nil {
		return Spec{}, err
	}
	s, err := readSpec(m.db.QueryRow(ctx, `SELECT `+specColumns+` FROM specs WHERE id = $1`, specID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Spec{}, ErrNotFound
	}
	if err != nil {
		return Spec{}, fmt.Errorf("get spec: %w", err)
	}
	return s, nil
}

// RenameSpec changes a spec's name. Owners, admins and editors may.
func (m *Module) RenameSpec(ctx context.Context, userID, specID uuid.UUID, pre workspaces.Precondition, name string) (Spec, error) {
	var errs validate.Errors
	name = validate.Name(&errs, "/name", name)
	if err := errs.Err(); err != nil {
		return Spec{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, edit); err != nil {
		return Spec{}, err
	}
	var s Spec
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := lockSpec(ctx, tx, specID, pre); err != nil {
			return err
		}
		var err error
		s, err = readSpec(tx.QueryRow(ctx, `
			UPDATE specs SET name = $2, version = version + 1, updated_at = now() WHERE id = $1 RETURNING `+specColumns, specID, name))
		return err
	})
	switch {
	case isModuleError(err):
		return Spec{}, err
	case err != nil:
		return Spec{}, fmt.Errorf("rename spec: %w", err)
	}
	return s, nil
}

// DeleteSpec deletes a spec with its revisions. Owners and admins may.
func (m *Module) DeleteSpec(ctx context.Context, userID, specID uuid.UUID, pre workspaces.Precondition) error {
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, remove); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := lockSpec(ctx, tx, specID, pre); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM specs WHERE id = $1`, specID)
		return err
	})
	switch {
	case isModuleError(err):
		return err
	case err != nil:
		return fmt.Errorf("delete spec: %w", err)
	}
	m.logger.InfoContext(ctx, "deleted spec", id.LogAttr("spec_id", id.Spec, specID), id.LogAttr("user_id", id.User, userID))
	return nil
}

// lockSpec locks a spec row and checks the precondition against it.
func lockSpec(ctx context.Context, tx pgx.Tx, specID uuid.UUID, pre workspaces.Precondition) error {
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM specs WHERE id = $1 FOR UPDATE`, specID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !pre.Matches(version) {
		return ErrVersionMismatch
	}
	return nil
}

// CreateRevision saves content as the spec's new current revision. The
// precondition is required: it must name the spec's current version, so two
// people editing at once can't overwrite each other's work. Owners, admins
// and editors may.
func (m *Module) CreateRevision(ctx context.Context, userID, specID uuid.UUID, pre workspaces.Precondition, content, msg string) (Spec, Revision, error) {
	if len(pre.Versions) == 0 {
		return Spec{}, Revision{}, ErrPreconditionRequired
	}
	var errs validate.Errors
	msg = message(&errs, msg)
	doc, err := m.check(ctx, content, &errs)
	if err != nil {
		return Spec{}, Revision{}, err
	}
	if err := errs.Err(); err != nil {
		return Spec{}, Revision{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, edit); err != nil {
		return Spec{}, Revision{}, err
	}
	var (
		s   Spec
		rev Revision
	)
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := lockSpec(ctx, tx, specID, pre); err != nil {
			return err
		}
		var number int
		if err := tx.QueryRow(ctx, `SELECT next_revision FROM specs WHERE id = $1`, specID).Scan(&number); err != nil {
			return err
		}
		var err error
		if rev, err = insertRevision(ctx, tx, specID, number, msg, doc, userID); err != nil {
			return err
		}
		s, err = readSpec(tx.QueryRow(ctx, `
			UPDATE specs SET current_revision_id = $2, next_revision = next_revision + 1, version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+specColumns, specID, rev.ID))
		return err
	})
	switch {
	case isModuleError(err):
		return Spec{}, Revision{}, err
	case err != nil:
		return Spec{}, Revision{}, fmt.Errorf("create revision: %w", err)
	}
	m.logger.InfoContext(ctx, "saved spec revision", id.LogAttr("revision_id", id.Revision, rev.ID),
		id.LogAttr("spec_id", id.Spec, specID), "number", rev.Number, "errors", rev.Counts.Errors, id.LogAttr("user_id", id.User, userID))
	return s, rev, nil
}

const revisionColumns = `id, spec_id, number, message, file_type, format, openapi_version, title, api_version,
	size_bytes, sha256, errors, warnings, infos, hints, created_by, created_at`

func readRevision(row pgx.Row) (Revision, error) {
	var (
		r   Revision
		sha []byte
	)
	err := row.Scan(&r.ID, &r.SpecID, &r.Number, &r.Message, &r.FileType, &r.Format, &r.OpenAPIVersion, &r.Title, &r.APIVersion,
		&r.SizeBytes, &sha, &r.Counts.Errors, &r.Counts.Warnings, &r.Counts.Infos, &r.Counts.Hints, &r.CreatedBy, &r.CreatedAt)
	copy(r.SHA256[:], sha)
	return r, err
}

// ListRevisions lists a spec's revisions, newest first. p.After is the last
// revision of the previous page; the page holds older ones.
func (m *Module) ListRevisions(ctx context.Context, userID, specID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[Revision], error) {
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, read); err != nil {
		return workspaces.Page[Revision]{}, err
	}
	limit := limitOf(p)
	rows, err := m.db.Query(ctx, `
		SELECT `+revisionColumns+` FROM spec_revisions
		WHERE spec_id = $1 AND ($2::uuid IS NULL OR number < (SELECT number FROM spec_revisions WHERE id = $2 AND spec_id = $1))
		ORDER BY number DESC LIMIT $3`, specID, afterArg(p.After), limit+1)
	if err != nil {
		return workspaces.Page[Revision]{}, fmt.Errorf("list revisions: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Revision, error) { return readRevision(row) })
	if err != nil {
		return workspaces.Page[Revision]{}, fmt.Errorf("list revisions: %w", err)
	}
	return page(items, limit), nil
}

// GetRevision returns a revision's details.
func (m *Module) GetRevision(ctx context.Context, userID, revisionID uuid.UUID) (Revision, error) {
	if err := m.authorizeFor(ctx, userID, projectOfRevision, revisionID, read); err != nil {
		return Revision{}, err
	}
	r, err := readRevision(m.db.QueryRow(ctx, `SELECT `+revisionColumns+` FROM spec_revisions WHERE id = $1`, revisionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("get revision: %w", err)
	}
	return r, nil
}

// RevisionContent returns a revision's document exactly as it was saved,
// and whether it is yaml or json.
func (m *Module) RevisionContent(ctx context.Context, userID, revisionID uuid.UUID) (content, fileType string, err error) {
	if err := m.authorizeFor(ctx, userID, projectOfRevision, revisionID, read); err != nil {
		return "", "", err
	}
	err = m.db.QueryRow(ctx, `SELECT content, file_type FROM spec_revisions WHERE id = $1`, revisionID).Scan(&content, &fileType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("get revision content: %w", err)
	}
	return content, fileType, nil
}

// RevisionReport returns the check report recorded with a revision.
func (m *Module) RevisionReport(ctx context.Context, userID, revisionID uuid.UUID) (specengine.Report, error) {
	if err := m.authorizeFor(ctx, userID, projectOfRevision, revisionID, read); err != nil {
		return specengine.Report{}, err
	}
	var raw []byte
	err := m.db.QueryRow(ctx, `SELECT report FROM spec_revisions WHERE id = $1`, revisionID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return specengine.Report{}, ErrNotFound
	}
	if err != nil {
		return specengine.Report{}, fmt.Errorf("get revision report: %w", err)
	}
	var report specengine.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return specengine.Report{}, fmt.Errorf("read revision report: %w", err)
	}
	return report, nil
}
