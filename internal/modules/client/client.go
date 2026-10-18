// SPDX-License-Identifier: Apache-2.0

// Package client is the module for the API client: sending requests from
// a project with its environments' variables, each person's history of
// what they sent, and collections of saved requests. Requests run in the
// runner (internal/modules/runner); this module decides who may send what,
// and keeps the record.
// See docs/rfcs/0001-api-client-and-tests.md.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/secretbox"
)

// Errors the module returns.
var (
	// ErrNotFound means the project or entry doesn't exist, or the user
	// can't see it.
	ErrNotFound = errors.New("client: not found")
	// ErrForbidden means the user's role doesn't allow sending requests.
	ErrForbidden = errors.New("client: not allowed")
	// ErrRunnerUnavailable means no runner can send the request: the
	// process doesn't run the runner role.
	ErrRunnerUnavailable = errors.New("client: no runner available")
)

// Access tells the module what a user may do in a project.
type Access interface {
	ProjectRole(ctx context.Context, userID, projectID uuid.UUID) (workspaces.Role, error)
	GetEnvironment(ctx context.Context, userID, environmentID uuid.UUID) (workspaces.Environment, workspaces.Role, error)
}

// Runner sends a request. In one process it is the runner module; when the
// roles run apart it will be a NATS client.
type Runner interface {
	Run(ctx context.Context, req runner.Request) runner.Result
}

// What history keeps.
const (
	historyDays         = 30
	historyPerPerson    = 1000
	maxStoredRequestRaw = 64 << 10
	maxStoredResponse   = 1 << 20
	// redacted replaces the values of credential headers in history.
	redacted = "•••"
)

// credentialHeaders are headers whose values history doesn't keep.
var credentialHeaders = map[string]bool{"authorization": true, "proxy-authorization": true, "cookie": true}

// Module sends requests and keeps their history.
type Module struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	access Access
	runner Runner
	// kek encrypts workspaces' data keys; nil when no secret key is
	// configured.
	kek *secretbox.Box
}

// New returns the module. run may be nil, when this process can't send
// requests.
func New(db *pgxpool.Pool, logger *slog.Logger, access Access, run Runner) *Module {
	return &Module{db: db, logger: logger, access: access, runner: run}
}

// Entry is one request someone sent, as history keeps it.
type Entry struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
	// EnvironmentID is the environment whose variables were filled in, or
	// uuid.Nil.
	EnvironmentID uuid.UUID
	// Unresolved lists the {{names}} that had no value, sorted; they were
	// sent as they are.
	Unresolved []string
	Method     string
	// URL is as sent. In history, secrets' values are replaced by •••
	// wherever they appear.
	URL string
	// Headers are as sent, with credentials replaced by •••.
	Headers []runner.Header
	// Body is the request body, at most 64 KiB of it.
	Body          []byte
	BodyTruncated bool
	Result        runner.Result
	SentAt        time.Time
}

// Summary is an entry without its bodies and headers, for lists.
type Summary struct {
	ID        uuid.UUID
	Method    string
	URL       string
	Status    int // 0 when the request failed
	Failure   string
	Duration  time.Duration
	SentAt    time.Time
	ProjectID uuid.UUID
}

func (m *Module) authorize(ctx context.Context, userID, projectID uuid.UUID, mayEdit bool) error {
	role, err := m.access.ProjectRole(ctx, userID, projectID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	if mayEdit && !role.CanEdit() {
		return ErrForbidden
	}
	return nil
}

// Send sends a request from a project and keeps it in the sender's
// history. Owners, admins and editors may: the server sends it on their
// behalf, to whatever their environments point at. With an environment of
// the project, its variables fill in {{name}} first. The result comes back
// whole; history keeps the first megabyte of the response body, and masks
// the environment's secrets.
func (m *Module) Send(ctx context.Context, userID, projectID, environmentID uuid.UUID, req runner.Request) (Entry, error) {
	if err := m.authorize(ctx, userID, projectID, true); err != nil {
		return Entry{}, err
	}
	if m.runner == nil {
		return Entry{}, ErrRunnerUnavailable
	}
	var (
		values  map[string]string
		secrets []string
	)
	if environmentID != uuid.Nil {
		env, err := m.environment(ctx, userID, environmentID, read)
		if err != nil {
			return Entry{}, err
		}
		if env.ProjectID != projectID {
			return Entry{}, ErrNotFound
		}
		if values, secrets, err = m.environmentValues(ctx, env); err != nil {
			return Entry{}, err
		}
	}
	res := newResolver(values, secrets, time.Now())
	req, err := res.request(req)
	if err != nil {
		return Entry{}, err
	}
	result := m.runner.Run(ctx, req)
	e := Entry{
		ID: id.New(), ProjectID: projectID, UserID: userID, EnvironmentID: environmentID, Unresolved: res.Unresolved(),
		Method: strings.ToUpper(strings.TrimSpace(req.Method)), URL: strings.TrimSpace(req.URL),
		Headers: redact(req.Headers), Body: req.Body, Result: result,
	}
	// What comes back is whole; what history keeps doesn't hold secrets.
	kept := e
	mask := newMasker(values, secrets)
	kept.URL, kept.Headers, kept.Body = mask.string(kept.URL), mask.headers(kept.Headers), mask.bytes(kept.Body)
	if f := result.Failure; f != nil {
		// Failure messages can name the host, which a secret may fill in.
		masked := *f
		masked.Message = mask.string(f.Message)
		kept.Result.Failure = &masked
	}
	if r := result.Response; r != nil {
		masked := *r
		// Masked before save cuts it, so the cut can't leave part of a
		// secret.
		masked.Headers, masked.URL, masked.Body = mask.headers(r.Headers), mask.string(r.URL), mask.bytes(r.Body)
		kept.Result.Response = &masked
	}
	if len(kept.Body) > maxStoredRequestRaw {
		// Cut on a character boundary, so a text body stays text.
		n := maxStoredRequestRaw
		for n > 0 && !utf8.RuneStart(kept.Body[n]) {
			n--
		}
		kept.Body, kept.BodyTruncated = kept.Body[:n], true
	}
	if err := m.save(ctx, &kept); err != nil {
		// The request was sent; losing its history entry isn't worth
		// failing the send over.
		m.logger.WarnContext(ctx, "couldn't keep a sent request in history", "error", err, id.LogAttr("user_id", id.User, userID))
	}
	e.SentAt = kept.SentAt
	return e, nil
}

func redact(headers []runner.Header) []runner.Header {
	out := make([]runner.Header, len(headers))
	for i, h := range headers {
		out[i] = h
		if credentialHeaders[strings.ToLower(strings.TrimSpace(h.Key))] && h.Value != "" {
			out[i].Value = redacted
		}
	}
	return out
}

// stored is what history keeps of a result, as JSON.
type stored struct {
	EnvironmentID *uuid.UUID      `json:"environmentId,omitempty"`
	Unresolved    []string        `json:"unresolved,omitempty"`
	Headers       []runner.Header `json:"headers"`
	Body          string          `json:"body,omitempty"`
	BodyTruncated bool            `json:"bodyTruncated,omitempty"`
	Failure       *runner.Failure `json:"failure,omitempty"`
	Response      *storedResponse `json:"response,omitempty"`
	Timings       storedTimings   `json:"timings"`
}

type storedResponse struct {
	Status     int             `json:"status"`
	StatusText string          `json:"statusText"`
	Proto      string          `json:"proto"`
	Headers    []runner.Header `json:"headers"`
	URL        string          `json:"url"`
	Redirects  int             `json:"redirects"`
	RemoteAddr string          `json:"remoteAddr"`
	// Truncated says the body was cut, by the runner or by history.
	Truncated bool `json:"truncated"`
}

type storedTimings struct {
	DNS, Connect, TLS, FirstByte, Download, Total int64 // microseconds
}

func (m *Module) save(ctx context.Context, e *Entry) error {
	s := stored{Headers: e.Headers, Unresolved: e.Unresolved, BodyTruncated: e.BodyTruncated, Failure: e.Result.Failure, Timings: timingsOut(e.Result.Timings)}
	if e.EnvironmentID != uuid.Nil {
		s.EnvironmentID = &e.EnvironmentID
	}
	if utf8.Valid(e.Body) {
		s.Body = string(e.Body)
	}
	var (
		status   *int
		bodyKept []byte
	)
	if r := e.Result.Response; r != nil {
		status = &r.Status
		bodyKept = r.Body
		truncated := r.Truncated
		if len(bodyKept) > maxStoredResponse {
			bodyKept, truncated = bodyKept[:maxStoredResponse], true
		}
		s.Response = &storedResponse{Status: r.Status, StatusText: r.StatusText, Proto: r.Proto, Headers: r.Headers, URL: r.URL,
			Redirects: r.Redirects, RemoteAddr: r.RemoteAddr, Truncated: truncated}
	}
	doc, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO request_history (id, project_id, user_id, method, url, status, record, response_body, duration_us)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING sent_at`,
			e.ID, e.ProjectID, e.UserID, e.Method, truncate(e.URL, 8192), status, doc, bodyKept, e.Result.Timings.Total.Microseconds(),
		).Scan(&e.SentAt); err != nil {
			return err
		}
		// Each person keeps their newest entries in each project.
		_, err := tx.Exec(ctx, `
			DELETE FROM request_history WHERE id IN (
				SELECT id FROM request_history WHERE project_id = $1 AND user_id = $2
				ORDER BY id DESC OFFSET $3)`, e.ProjectID, e.UserID, historyPerPerson)
		return err
	})
}

func timingsOut(t runner.Timings) storedTimings {
	return storedTimings{t.DNS.Microseconds(), t.Connect.Microseconds(), t.TLS.Microseconds(), t.FirstByte.Microseconds(), t.Download.Microseconds(), t.Total.Microseconds()}
}

func timingsIn(t storedTimings) runner.Timings {
	us := func(v int64) time.Duration { return time.Duration(v) * time.Microsecond }
	return runner.Timings{DNS: us(t.DNS), Connect: us(t.Connect), TLS: us(t.TLS), FirstByte: us(t.FirstByte), Download: us(t.Download), Total: us(t.Total)}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// HistoryPage is one page of a person's history, newest first.
type HistoryPage = workspaces.Page[Summary]

// History lists what the person sent from a project, newest first. People
// see only their own history.
func (m *Module) History(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (HistoryPage, error) {
	if err := m.authorize(ctx, userID, projectID, false); err != nil {
		return HistoryPage{}, err
	}
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var after any
	if p.After != uuid.Nil {
		after = p.After
	}
	rows, err := m.db.Query(ctx, `
		SELECT id, project_id, method, url, coalesce(status, 0), coalesce(record->'failure'->>'message', ''), duration_us, sent_at
		FROM request_history
		WHERE project_id = $1 AND user_id = $2 AND ($3::uuid IS NULL OR id < $3) AND sent_at > now() - $4 * interval '1 day'
		ORDER BY id DESC LIMIT $5`, projectID, userID, after, historyDays, limit+1)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("list history: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var s Summary
		var us int64
		err := row.Scan(&s.ID, &s.ProjectID, &s.Method, &s.URL, &s.Status, &s.Failure, &us, &s.SentAt)
		s.Duration = time.Duration(us) * time.Microsecond
		return s, err
	})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("list history: %w", err)
	}
	page := HistoryPage{Items: items}
	if len(items) > limit {
		page.Items, page.More = items[:limit], true
	}
	return page, nil
}

// HistoryEntry returns one of the person's own entries.
func (m *Module) HistoryEntry(ctx context.Context, userID, entryID uuid.UUID) (Entry, error) {
	var (
		e    Entry
		doc  []byte
		body []byte
	)
	err := m.db.QueryRow(ctx, `
		SELECT id, project_id, user_id, method, url, record, response_body, sent_at
		FROM request_history WHERE id = $1 AND user_id = $2 AND sent_at > now() - $3 * interval '1 day'`,
		entryID, userID, historyDays).Scan(&e.ID, &e.ProjectID, &e.UserID, &e.Method, &e.URL, &doc, &body, &e.SentAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get history entry: %w", err)
	}
	// A person removed from the project loses their history there.
	if err := m.authorize(ctx, userID, e.ProjectID, false); err != nil {
		return Entry{}, err
	}
	var s stored
	if err := json.Unmarshal(doc, &s); err != nil {
		return Entry{}, fmt.Errorf("read history entry: %w", err)
	}
	e.Headers, e.Body, e.BodyTruncated, e.Unresolved = s.Headers, []byte(s.Body), s.BodyTruncated, s.Unresolved
	if s.EnvironmentID != nil {
		e.EnvironmentID = *s.EnvironmentID
	}
	e.Result = runner.Result{Failure: s.Failure, Timings: timingsIn(s.Timings)}
	if r := s.Response; r != nil {
		e.Result.Response = &runner.Response{Status: r.Status, StatusText: r.StatusText, Proto: r.Proto, Headers: r.Headers,
			Body: body, Truncated: r.Truncated, URL: r.URL, Redirects: r.Redirects, RemoteAddr: r.RemoteAddr}
	}
	return e, nil
}

// RemoveOldHistory deletes entries older than 30 days, and returns how
// many.
func (m *Module) RemoveOldHistory(ctx context.Context) (int64, error) {
	tag, err := m.db.Exec(ctx, `DELETE FROM request_history WHERE sent_at < now() - $1 * interval '1 day'`, historyDays)
	if err != nil {
		return 0, fmt.Errorf("remove old history: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Run removes old history every hour until ctx ends.
func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := m.RemoveOldHistory(ctx); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "couldn't remove old request history", "error", err)
		} else if n > 0 {
			m.logger.InfoContext(ctx, "removed old request history", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
