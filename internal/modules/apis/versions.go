// SPDX-License-Identifier: Apache-2.0

package apis

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// ErrVersionExists means the spec already has a version with the name.
var ErrVersionExists = errors.New("apis: version name taken")

// HasErrorsError means a revision can't be published because the spec
// engine found errors in it. Warnings don't block publishing.
type HasErrorsError struct {
	Errors int
}

func (e *HasErrorsError) Error() string {
	return fmt.Sprintf("apis: the revision has %d errors", e.Errors)
}

// LatestVersion names the newest published version wherever a version name
// is expected, such as in a mock's URL. No version can be named latest.
const LatestVersion = "latest"

// Version is a published revision of a spec. Versions never change.
type Version struct {
	ID             uuid.UUID
	SpecID         uuid.UUID
	Name           string
	RevisionID     uuid.UUID
	RevisionNumber int
	// APIVersion is the revision's info.version.
	APIVersion  string
	Notes       string
	PublishedBy uuid.UUID
	PublishedAt time.Time
}

// VersionInput publishes a revision.
type VersionInput struct {
	// RevisionID is the revision to publish; uuid.Nil means the current one.
	RevisionID uuid.UUID
	// Name defaults to the revision's info.version.
	Name  string
	Notes string
}

const (
	maxVersionName = 64
	maxNotes       = 2000
)

var versionNamePattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)

const versionColumns = `v.id, v.spec_id, v.name, v.revision_id, r.number, r.api_version, v.notes, v.published_by, v.published_at`

func readVersion(row pgx.Row) (Version, error) {
	var v Version
	err := row.Scan(&v.ID, &v.SpecID, &v.Name, &v.RevisionID, &v.RevisionNumber, &v.APIVersion, &v.Notes, &v.PublishedBy, &v.PublishedAt)
	return v, err
}

// PublishVersion publishes a revision of the spec under a name. A revision
// with errors can't be published. Owners, admins and editors may.
func (m *Module) PublishVersion(ctx context.Context, userID, specID uuid.UUID, in VersionInput) (Version, error) {
	var errs validate.Errors
	in.Name = strings.TrimSpace(in.Name)
	in.Notes = strings.TrimSpace(in.Notes)
	if utf8.RuneCountInString(in.Notes) > maxNotes {
		errs.Add("/notes", "maxLength", fmt.Sprintf("Use at most %d characters.", maxNotes))
	}
	if in.Name != "" {
		versionName(&errs, in.Name)
	}
	if err := errs.Err(); err != nil {
		return Version{}, err
	}
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, edit); err != nil {
		return Version{}, err
	}
	var v Version
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		// Find the revision, which must belong to this spec.
		revID := in.RevisionID
		var (
			apiVersion string
			problems   int
		)
		err := tx.QueryRow(ctx, `
			SELECT r.id, r.api_version, r.errors FROM spec_revisions r JOIN specs s ON s.id = r.spec_id
			WHERE r.spec_id = $1 AND r.id = coalesce($2, s.current_revision_id)`,
			specID, afterArg(revID)).Scan(&revID, &apiVersion, &problems)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if problems > 0 {
			return &HasErrorsError{Errors: problems}
		}
		name := in.Name
		if name == "" {
			// The document's own version, which must make a valid name too.
			var nameErrs validate.Errors
			name = versionName(&nameErrs, strings.TrimSpace(apiVersion))
			if len(nameErrs) > 0 {
				nameErrs = validate.Errors{{Pointer: "/name", Code: "required",
					Detail: "Name the version: the document's info.version can't be used as one."}}
				return nameErrs
			}
		}
		vid := id.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO spec_versions (id, spec_id, name, revision_id, notes, published_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			vid, specID, name, revID, in.Notes, userID); err != nil {
			return err
		}
		v, err = readVersion(tx.QueryRow(ctx, `
			SELECT `+versionColumns+` FROM spec_versions v JOIN spec_revisions r ON r.id = v.revision_id WHERE v.id = $1`, vid))
		return err
	})
	var hasErrors *HasErrorsError
	var fields validate.Errors
	switch {
	case postgres.IsUniqueViolation(err, "spec_versions_spec_name_key"):
		return Version{}, ErrVersionExists
	case errors.As(err, &hasErrors), errors.As(err, &fields), isModuleError(err):
		return Version{}, err
	case err != nil:
		return Version{}, fmt.Errorf("publish version: %w", err)
	}
	m.logger.InfoContext(ctx, "published spec version", id.LogAttr("version_id", id.Version, v.ID), id.LogAttr("spec_id", id.Spec, specID),
		"name", v.Name, id.LogAttr("revision_id", id.Revision, v.RevisionID), id.LogAttr("user_id", id.User, userID))
	return v, nil
}

func versionName(errs *validate.Errors, name string) string {
	switch {
	case name == "":
		errs.Add("/name", "required", "Name the version, such as 1.4.0.")
	case utf8.RuneCountInString(name) > maxVersionName:
		errs.Add("/name", "maxLength", fmt.Sprintf("Use at most %d characters.", maxVersionName))
	case strings.EqualFold(name, LatestVersion):
		errs.Add("/name", "pattern", "latest always means the newest version, so it can't name one.")
	case !versionNamePattern.MatchString(name):
		errs.Add("/name", "pattern", "Use letters, digits, dots, hyphens, underscores and plus signs, starting with a letter or digit, such as 1.4.0.")
	}
	return name
}

// ListVersions lists a spec's versions, newest first. p.After is the last
// version of the previous page.
func (m *Module) ListVersions(ctx context.Context, userID, specID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[Version], error) {
	if err := m.authorizeFor(ctx, userID, projectOfSpec, specID, read); err != nil {
		return workspaces.Page[Version]{}, err
	}
	limit := limitOf(p)
	rows, err := m.db.Query(ctx, `
		SELECT `+versionColumns+` FROM spec_versions v JOIN spec_revisions r ON r.id = v.revision_id
		WHERE v.spec_id = $1 AND ($2::uuid IS NULL OR v.id < $2)
		ORDER BY v.id DESC LIMIT $3`, specID, afterArg(p.After), limit+1)
	if err != nil {
		return workspaces.Page[Version]{}, fmt.Errorf("list versions: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Version, error) { return readVersion(row) })
	if err != nil {
		return workspaces.Page[Version]{}, fmt.Errorf("list versions: %w", err)
	}
	return page(items, limit), nil
}

const projectOfVersion = `SELECT s.project_id FROM spec_versions v JOIN specs p ON p.id = v.spec_id JOIN services s ON s.id = p.service_id WHERE v.id = $1`

// GetVersion returns a published version.
func (m *Module) GetVersion(ctx context.Context, userID, versionID uuid.UUID) (Version, error) {
	if err := m.authorizeFor(ctx, userID, projectOfVersion, versionID, read); err != nil {
		return Version{}, err
	}
	v, err := readVersion(m.db.QueryRow(ctx, `
		SELECT `+versionColumns+` FROM spec_versions v JOIN spec_revisions r ON r.id = v.revision_id WHERE v.id = $1`, versionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, fmt.Errorf("get version: %w", err)
	}
	return v, nil
}

// MockSource returns the revision a published version of a spec publishes,
// and its document, for serving the version's mock. name is a version name
// or LatestVersion. Mocks are open to anyone who can reach the server, so
// this checks no user; ErrNotFound means there's no such version.
func (m *Module) MockSource(ctx context.Context, specID uuid.UUID, name string) (revisionID uuid.UUID, content string, err error) {
	query := `
		SELECT r.id, r.content FROM spec_versions v JOIN spec_revisions r ON r.id = v.revision_id
		WHERE v.spec_id = $1 AND v.name = $2`
	args := []any{specID, name}
	if strings.EqualFold(name, LatestVersion) {
		query = `
			SELECT r.id, r.content FROM spec_versions v JOIN spec_revisions r ON r.id = v.revision_id
			WHERE v.spec_id = $1 ORDER BY v.id DESC LIMIT 1`
		args = []any{specID}
	}
	err = m.db.QueryRow(ctx, query, args...).Scan(&revisionID, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("find mock source: %w", err)
	}
	return revisionID, content, nil
}
