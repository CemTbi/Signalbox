// SPDX-License-Identifier: Apache-2.0

// Package monitors checks a project's APIs on a schedule and keeps what it
// finds: each monitor's current state in PostgreSQL, and every result in
// ClickHouse. See docs/adr/0046-monitors.md.
package monitors

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/token"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Errors the module returns. Validation problems come back as
// validate.Errors.
var (
	// ErrNotFound means the monitor, project, environment or service doesn't
	// exist, or the user can't see it.
	ErrNotFound = errors.New("monitors: not found")
	// ErrForbidden means the user's role doesn't allow the change.
	ErrForbidden = errors.New("monitors: not allowed")
	// ErrVersionMismatch means the monitor changed since the version in the
	// precondition.
	ErrVersionMismatch = errors.New("monitors: version mismatch")
	// ErrNotCheckable means the monitor can't be checked by hand: a
	// heartbeat is checked by the calls its job makes.
	ErrNotCheckable = errors.New("monitors: checked by its heartbeats")
	// ErrNoHeartbeat means the monitor isn't a heartbeat, so has no token.
	ErrNoHeartbeat = errors.New("monitors: not a heartbeat")
)

// The kinds of monitor. A health monitor also reads the body of its
// response, as a health format. A collection monitor runs a collection, or
// a folder of it. A heartbeat sends nothing: a job calls its URL.
const (
	KindHTTP       = "http"
	KindHealth     = "health"
	KindCollection = "collection"
	KindHeartbeat  = "heartbeat"
)

// HeartbeatTokenPrefix starts every heartbeat token, so secret scanners and
// people can tell one apart from other secrets.
const HeartbeatTokenPrefix = "sbx_hb_"

// A monitor's states.
const (
	StatePending  = "pending"
	StateUp       = "up"
	StateDegraded = "degraded"
	StateDown     = "down"
	StatePaused   = "paused"
)

// What a monitor may be set to.
const (
	MinInterval = 30 * time.Second
	MaxInterval = time.Hour
	// MaxTimeout is the runner's limit.
	MaxTimeout = 30 * time.Second
	// MaxRunTimeout bounds a collection monitor's whole run.
	MaxRunTimeout = 2 * time.Minute
	// A heartbeat's interval may be longer, for jobs that run daily or
	// monthly, and its grace a week.
	MinHeartbeatInterval = time.Minute
	MaxHeartbeatInterval = 30 * 24 * time.Hour
	MaxGrace             = 7 * 24 * time.Hour

	defaultFailures    = 2
	maxFailures        = 10
	defaultCertWarning = 14
	maxCertWarning     = 365
	maxAssertions      = 20
	// maxPerProject bounds what one project asks of the scheduler, and lets
	// the project's statistics read all its monitors at once.
	maxPerProject = 1000
)

// Monitor checks one request on a schedule.
type Monitor struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	// ServiceID places its health on the canvas; uuid.Nil for none.
	ServiceID uuid.UUID
	Name      string
	Kind      string
	// Request and Assertions are an HTTP or health monitor's.
	Request    client.SavedRequest
	Assertions []Assertion
	// CollectionID is what a collection monitor runs, and FolderID the one
	// folder of it, or uuid.Nil for all of it.
	CollectionID uuid.UUID
	FolderID     uuid.UUID
	Interval     time.Duration
	// Timeout bounds a request, or a collection monitor's whole run; a
	// heartbeat has none.
	Timeout time.Duration
	// Grace is how late past its interval a heartbeat may be.
	Grace time.Duration
	// LastPingAt is when its job last called a heartbeat; zero before.
	LastPingAt time.Time
	// HeartbeatToken is a heartbeat's token, only just after it was made:
	// it's stored as a hash.
	HeartbeatToken string
	// FailuresToConfirm is how many failures in a row make it down.
	FailuresToConfirm int
	// CertificateWarningDays turns it degraded when its server's certificate
	// expires this soon; 0 never does.
	CertificateWarningDays int
	State                  string
	ConsecutiveFailures    int
	NextCheckAt            time.Time
	// Last is what the last check found; nil before the first.
	Last *Last
	// CertificateExpiresAt is when the server's certificate expires, as of
	// the last HTTPS response; zero when unknown.
	CertificateExpiresAt time.Time
	// Version increments on every change people make; checks don't.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Last is what a monitor's last check found.
type Last struct {
	At             time.Time
	Outcome        string
	Status         int
	Duration       time.Duration
	FailureCode    string
	FailureMessage string
	TraceID        string
	Components     []Component
	// RunID is a collection monitor's test run; uuid.Nil for others.
	RunID uuid.UUID
}

// Input is a new monitor. Zero values take the defaults where there are any.
type Input struct {
	Name                   string
	Kind                   string
	EnvironmentID          uuid.UUID
	ServiceID              uuid.UUID
	Request                client.SavedRequest
	Assertions             []Assertion
	CollectionID           uuid.UUID
	FolderID               uuid.UUID
	Interval               time.Duration
	Timeout                time.Duration
	Grace                  *time.Duration
	FailuresToConfirm      *int
	CertificateWarningDays *int
	Paused                 bool
}

// Patch changes the fields that aren't nil.
type Patch struct {
	Name *string
	Kind *string
	// ServiceID is a pointer to uuid.Nil to take the monitor off the canvas.
	ServiceID  *uuid.UUID
	Request    *client.SavedRequest
	Assertions *[]Assertion
	// CollectionID changes what a collection monitor runs, and FolderID is
	// a pointer to uuid.Nil to run all of it.
	CollectionID           *uuid.UUID
	FolderID               *uuid.UUID
	Interval               *time.Duration
	Timeout                *time.Duration
	Grace                  *time.Duration
	FailuresToConfirm      *int
	CertificateWarningDays *int
	Paused                 *bool
}

// Access tells the module what a user may do in a project.
type Access interface {
	ProjectRole(ctx context.Context, userID, projectID uuid.UUID) (workspaces.Role, error)
	GetEnvironment(ctx context.Context, userID, environmentID uuid.UUID) (workspaces.Environment, workspaces.Role, error)
	// Environment reads an environment for the scheduler, which acts for no
	// one.
	Environment(ctx context.Context, environmentID uuid.UUID) (workspaces.Environment, error)
}

// Services finds a project's services.
type Services interface {
	GetService(ctx context.Context, userID, serviceID uuid.UUID) (apis.Service, error)
}

// Prober sends a monitor's request.
type Prober interface {
	Probe(ctx context.Context, env workspaces.Environment, req client.SavedRequest, timeout time.Duration) (client.Entry, error)
}

// Collections finds the collection, and folder, a monitor runs;
// *client.Module is one.
type Collections interface {
	PlanFor(ctx context.Context, collectionID, folderID uuid.UUID) (client.Collection, client.Plan, error)
}

// Runs runs a collection monitor's collection; *testruns.Module is one.
type Runs interface {
	RunForMonitor(ctx context.Context, mr testruns.MonitorRun) (testruns.MonitorResult, error)
}

// Module keeps monitors and runs their checks.
type Module struct {
	db          *pgxpool.Pool
	conn        clickhouse.Conn
	logger      *slog.Logger
	access      Access
	services    Services
	prober      Prober
	collections Collections
	runs        Runs
	now         func() time.Time

	// slots bounds the checks running at once in this process.
	slots   chan struct{}
	running sync.WaitGroup

	mu      sync.Mutex
	pending []Check

	// observeDelay is told how late each check started, for measuring.
	observeDelay func(time.Duration)

	checks  *metrics.Counter
	delay   *metrics.Histogram
	dropped *metrics.Counter
}

// New returns the module. Run writes results and Schedule starts checks.
// Without runs, collection monitors' checks are skipped.
func New(db *pgxpool.Pool, conn clickhouse.Conn, logger *slog.Logger, access Access, services Services, prober Prober,
	collections Collections, runs Runs, reg *metrics.Registry,
) *Module {
	m := &Module{
		db: db, conn: conn, logger: logger, access: access, services: services, prober: prober, collections: collections, runs: runs, now: time.Now,
		slots: make(chan struct{}, concurrentChecks),
		checks: reg.NewCounter("signalbox_monitor_checks_total",
			"Monitor checks this process finished, by outcome: passed, degraded, failed or skipped.", "outcome"),
		delay: reg.NewHistogram("signalbox_monitor_check_start_delay_seconds",
			"How long after its time a monitor's check started.", metrics.DurationBuckets),
		dropped: reg.NewCounter("signalbox_monitor_results_dropped_total",
			"Check results dropped because ClickHouse couldn't take them for too long."),
	}
	reg.NewGaugeFunc("signalbox_monitor_checks_running", "Monitor checks running in this process.",
		func() float64 { return float64(len(m.slots)) })
	return m
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

const columns = `id, project_id, environment_id, coalesce(service_id, '00000000-0000-0000-0000-000000000000'), name, kind,
	request, assertions, interval_seconds, timeout_seconds, failures_to_confirm, certificate_warning_days, state, consecutive_failures,
	next_check_at, last_check_at, coalesce(last_outcome, ''), coalesce(last_status, 0), coalesce(last_duration_ms, 0),
	coalesce(last_failure_code, ''), coalesce(last_failure_message, ''), coalesce(last_trace_id, ''), last_components,
	certificate_expires_at, version, created_at, updated_at,
	coalesce(collection_id, '00000000-0000-0000-0000-000000000000'), coalesce(folder_id, '00000000-0000-0000-0000-000000000000'),
	grace_seconds, last_ping_at, coalesce(last_run_id, '00000000-0000-0000-0000-000000000000')`

func read(row pgx.Row, extra ...any) (Monitor, error) {
	var (
		mon                 Monitor
		request, assertions []byte
		components          []byte
		interval, timeout   int
		lastAt, certificate *time.Time
		outcome, code, msg  string
		status              int
		durationMs          float64
		trace               string
		grace               int
		lastPing            *time.Time
		runID               uuid.UUID
	)
	dest := []any{&mon.ID, &mon.ProjectID, &mon.EnvironmentID, &mon.ServiceID, &mon.Name, &mon.Kind,
		&request, &assertions, &interval, &timeout, &mon.FailuresToConfirm, &mon.CertificateWarningDays, &mon.State, &mon.ConsecutiveFailures,
		&mon.NextCheckAt, &lastAt, &outcome, &status, &durationMs, &code, &msg, &trace, &components,
		&certificate, &mon.Version, &mon.CreatedAt, &mon.UpdatedAt,
		&mon.CollectionID, &mon.FolderID, &grace, &lastPing, &runID}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Monitor{}, err
	}
	if err := json.Unmarshal(request, &mon.Request); err != nil {
		return Monitor{}, fmt.Errorf("read a monitor's request: %w", err)
	}
	if err := json.Unmarshal(assertions, &mon.Assertions); err != nil {
		return Monitor{}, fmt.Errorf("read a monitor's assertions: %w", err)
	}
	mon.Interval = time.Duration(interval) * time.Second
	mon.Timeout = time.Duration(timeout) * time.Second
	mon.Grace = time.Duration(grace) * time.Second
	if lastPing != nil {
		mon.LastPingAt = *lastPing
	}
	if certificate != nil {
		mon.CertificateExpiresAt = *certificate
	}
	if lastAt != nil {
		mon.Last = &Last{At: *lastAt, Outcome: outcome, Status: status, Duration: time.Duration(durationMs * float64(time.Millisecond)),
			FailureCode: code, FailureMessage: msg, TraceID: trace, RunID: runID}
		if len(components) > 0 {
			if err := json.Unmarshal(components, &mon.Last.Components); err != nil {
				return Monitor{}, fmt.Errorf("read a monitor's components: %w", err)
			}
		}
	}
	return mon, nil
}

// phase is where in its interval a monitor's checks fall, from its ID, so
// a thousand one-minute monitors don't all start at :00.
func phase(monitorID uuid.UUID, interval time.Duration) time.Duration {
	// IDs are UUIDv7, whose first bytes are the time: the last eight are
	// the random ones.
	v := binary.BigEndian.Uint64(monitorID[8:])
	return time.Duration(v % uint64(interval)) //nolint:gosec // G115: the interval is positive
}

// firstCheck is the monitor's next time after now, at its phase.
func firstCheck(monitorID uuid.UUID, interval time.Duration, now time.Time) time.Time {
	at := now.Truncate(interval).Add(phase(monitorID, interval))
	if !at.After(now) {
		at = at.Add(interval)
	}
	return at
}

func checkValues(errs *validate.Errors, mon *Monitor) {
	mon.Name = validate.Name(errs, "/name", mon.Name)
	if mon.Assertions == nil {
		mon.Assertions = []Assertion{}
	}
	switch mon.Kind {
	case KindHTTP, KindHealth:
		checkRequest(errs, mon)
	case KindCollection:
		checkCollection(errs, mon)
	case KindHeartbeat:
		checkHeartbeat(errs, mon)
	default:
		errs.Add("/kind", "enum", "Use http, health, collection or heartbeat.")
		return
	}
	if mon.Kind != KindHeartbeat && (mon.Interval < MinInterval || mon.Interval > MaxInterval || mon.Interval%time.Second != 0) {
		errs.Add("/intervalSeconds", "range", "Check every 30 to 3,600 seconds.")
	}
	if mon.FailuresToConfirm < 1 || mon.FailuresToConfirm > maxFailures {
		errs.Add("/failuresToConfirm", "range", fmt.Sprintf("Confirm with 1 to %d failures.", maxFailures))
	}
	if mon.Kind != KindHeartbeat && mon.Grace != 0 {
		errs.Add("/graceSeconds", "kind", "Only a heartbeat has a grace period.")
	}
	if mon.Kind != KindCollection && mon.FolderID != uuid.Nil {
		errs.Add("/folderId", "kind", "Only a collection monitor runs a folder.")
	}
}

func checkRequest(errs *validate.Errors, mon *Monitor) {
	if strings.TrimSpace(mon.Request.URL) == "" {
		errs.Add("/request/url", "required", "Enter the URL to check.")
	}
	client.CheckSaved(errs, &mon.Request)
	// A monitor's request runs for no one, so nothing checks it against a
	// spec version.
	mon.Request.Contract = nil
	if mon.CollectionID != uuid.Nil {
		errs.Add("/collectionId", "kind", "Only a collection monitor runs a collection.")
	}
	if mon.Timeout < time.Second || mon.Timeout > MaxTimeout || mon.Timeout%time.Second != 0 {
		errs.Add("/timeoutSeconds", "range", "Use a timeout of 1 to 30 seconds.")
	}
	if mon.CertificateWarningDays < 0 || mon.CertificateWarningDays > maxCertWarning {
		errs.Add("/certificateWarningDays", "range", fmt.Sprintf("Warn 0 to %d days before a certificate expires.", maxCertWarning))
	}
	if len(mon.Assertions) > maxAssertions {
		errs.Add("/assertions", "maxItems", fmt.Sprintf("Use at most %d assertions.", maxAssertions))
	}
	for i := range mon.Assertions {
		checkAssertion(errs, fmt.Sprintf("/assertions/%d", i), &mon.Assertions[i])
	}
}

// noRequest checks that a monitor that sends no request of its own has
// none: a collection monitor sends its collection's, and a heartbeat none.
func noRequest(errs *validate.Errors, mon *Monitor, why string) {
	r := mon.Request
	if r.URL != "" || r.Method != "" || len(r.Headers) > 0 || r.Body != "" || r.Auth != nil {
		errs.Add("/request", "kind", why)
	}
	if len(mon.Assertions) > 0 {
		errs.Add("/assertions", "kind", why)
	}
	if mon.CertificateWarningDays != 0 {
		errs.Add("/certificateWarningDays", "kind", "Only an HTTP or health monitor reads certificates.")
	}
}

func checkCollection(errs *validate.Errors, mon *Monitor) {
	noRequest(errs, mon, "A collection monitor sends its collection's requests, and their tests are its assertions.")
	if mon.CollectionID == uuid.Nil {
		errs.Add("/collectionId", "required", "Choose the collection to run.")
	}
	if mon.Timeout < time.Second || mon.Timeout > MaxRunTimeout || mon.Timeout%time.Second != 0 {
		errs.Add("/timeoutSeconds", "range", "Give the run 1 to 120 seconds.")
	}
}

func checkHeartbeat(errs *validate.Errors, mon *Monitor) {
	noRequest(errs, mon, "A heartbeat sends nothing: a job calls its URL.")
	if mon.CollectionID != uuid.Nil {
		errs.Add("/collectionId", "kind", "Only a collection monitor runs a collection.")
	}
	if mon.Interval < MinHeartbeatInterval || mon.Interval > MaxHeartbeatInterval || mon.Interval%time.Second != 0 {
		errs.Add("/intervalSeconds", "range", "Expect a heartbeat every minute to every 30 days.")
	}
	if mon.Timeout != 0 {
		errs.Add("/timeoutSeconds", "kind", "A heartbeat has no timeout: its grace period says how late it may be.")
	}
	if mon.Grace < 0 || mon.Grace > MaxGrace || mon.Grace%time.Second != 0 {
		errs.Add("/graceSeconds", "range", "Allow 0 seconds to 7 days of grace.")
	}
	// A missed heartbeat is missed: there's nothing to check again.
	if mon.FailuresToConfirm != 1 {
		errs.Add("/failuresToConfirm", "kind", "A heartbeat goes down at the first one missed, once its grace is over.")
	}
}

// checkPlace checks that the monitor's environment and service are its
// project's.
func (m *Module) checkPlace(ctx context.Context, userID uuid.UUID, mon *Monitor) error {
	env, _, err := m.access.GetEnvironment(ctx, userID, mon.EnvironmentID)
	if errors.Is(err, workspaces.ErrNotFound) || err == nil && env.ProjectID != mon.ProjectID {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check the environment: %w", err)
	}
	if mon.CollectionID != uuid.Nil {
		// Whoever may see the project may see its collections.
		col, _, err := m.collections.PlanFor(ctx, mon.CollectionID, mon.FolderID)
		if errors.Is(err, client.ErrNotFound) || err == nil && col.ProjectID != mon.ProjectID {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("check the collection: %w", err)
		}
	}
	if mon.ServiceID == uuid.Nil {
		return nil
	}
	svc, err := m.services.GetService(ctx, userID, mon.ServiceID)
	if errors.Is(err, apis.ErrNotFound) || err == nil && svc.ProjectID != mon.ProjectID {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check the service: %w", err)
	}
	return nil
}

func nullableID(v uuid.UUID) *uuid.UUID {
	if v == uuid.Nil {
		return nil
	}
	return &v
}

// Create adds a monitor to a project. Owners, admins and editors may. Its
// first check comes at its place in the first interval.
func (m *Module) Create(ctx context.Context, userID, projectID uuid.UUID, in Input) (Monitor, error) {
	mon := Monitor{
		ID: id.New(), ProjectID: projectID, EnvironmentID: in.EnvironmentID, ServiceID: in.ServiceID, Name: in.Name, Kind: in.Kind,
		Request: in.Request, Assertions: in.Assertions, CollectionID: in.CollectionID, FolderID: in.FolderID,
		Interval: in.Interval, Timeout: in.Timeout, State: StatePending,
	}
	if mon.Kind == "" {
		mon.Kind = KindHTTP
	}
	defaults(&mon, in)
	if in.Paused {
		mon.State = StatePaused
	}
	var errs validate.Errors
	checkValues(&errs, &mon)
	if err := errs.Err(); err != nil {
		return Monitor{}, err
	}
	if err := m.authorize(ctx, userID, projectID, true); err != nil {
		return Monitor{}, err
	}
	if err := m.checkPlace(ctx, userID, &mon); err != nil {
		return Monitor{}, err
	}
	request, err := json.Marshal(mon.Request)
	if err != nil {
		return Monitor{}, fmt.Errorf("encode the request: %w", err)
	}
	assertions, err := json.Marshal(mon.Assertions)
	if err != nil {
		return Monitor{}, fmt.Errorf("encode the assertions: %w", err)
	}
	now := m.now()
	next := firstCheck(mon.ID, mon.Interval, now)
	var secret string
	var tokenHash []byte
	if mon.Kind == KindHeartbeat {
		secret = HeartbeatTokenPrefix + token.Random()
		tokenHash = token.Hash(secret)
		next = heartbeatDue(mon, now, true)
	}
	var created Monitor
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		// A guard rail, not a promise: two created at once may pass it.
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE project_id = $1`, projectID).Scan(&count); err != nil {
			return err
		}
		if count >= maxPerProject {
			var errs validate.Errors
			errs.Add("/name", "maxItems", fmt.Sprintf("A project can have at most %d monitors. Delete one first.", maxPerProject))
			return errs.Err()
		}
		var err error
		created, err = read(tx.QueryRow(ctx, `
			INSERT INTO monitors (id, project_id, environment_id, service_id, name, kind, request, assertions, interval_seconds, timeout_seconds,
				failures_to_confirm, certificate_warning_days, state, next_check_at, created_by, collection_id, folder_id, grace_seconds, heartbeat_token_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
			RETURNING `+columns,
			mon.ID, projectID, mon.EnvironmentID, nullableID(mon.ServiceID), mon.Name, mon.Kind, request, assertions,
			int(mon.Interval/time.Second), int(mon.Timeout/time.Second), mon.FailuresToConfirm, mon.CertificateWarningDays, mon.State,
			next, userID, nullableID(mon.CollectionID), nullableID(mon.FolderID), int(mon.Grace/time.Second), tokenHash))
		return err
	})
	created.HeartbeatToken = secret
	var fields validate.Errors
	if errors.As(err, &fields) {
		return Monitor{}, err
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("create a monitor: %w", err)
	}
	m.logger.InfoContext(ctx, "created a monitor", id.LogAttr("monitor_id", id.Monitor, created.ID), id.LogAttr("user_id", id.User, userID))
	return created, nil
}

// defaults fills in what a new monitor of its kind takes when it isn't
// given.
func defaults(mon *Monitor, in Input) {
	mon.FailuresToConfirm, mon.CertificateWarningDays = defaultFailures, defaultCertWarning
	if mon.Interval == 0 {
		mon.Interval = time.Minute
	}
	switch mon.Kind {
	case KindCollection:
		mon.CertificateWarningDays = 0
		if mon.Timeout == 0 {
			mon.Timeout = time.Minute
		}
	case KindHeartbeat:
		mon.FailuresToConfirm, mon.CertificateWarningDays = 1, 0
		if in.Interval == 0 {
			mon.Interval = 24 * time.Hour
		}
		// As long as the interval, up to an hour: a nightly job may run late.
		mon.Grace = min(mon.Interval, time.Hour)
	default:
		if mon.Timeout == 0 {
			mon.Timeout = 10 * time.Second
		}
	}
	if in.FailuresToConfirm != nil {
		mon.FailuresToConfirm = *in.FailuresToConfirm
	}
	if in.CertificateWarningDays != nil {
		mon.CertificateWarningDays = *in.CertificateWarningDays
	}
	if in.Grace != nil {
		mon.Grace = *in.Grace
	}
}

// heartbeatDue is when a heartbeat is missed: its interval and grace after
// its last call, or after now when it never had one or was just resumed. A
// time already gone is now.
func heartbeatDue(mon Monitor, now time.Time, fresh bool) time.Time {
	base := now
	if !fresh && !mon.LastPingAt.IsZero() {
		base = mon.LastPingAt
	}
	if due := base.Add(mon.Interval + mon.Grace); due.After(now) {
		return due
	}
	return now
}

// sendsRequest says whether a kind of monitor sends a request of its own.
func sendsRequest(kind string) bool { return kind == KindHTTP || kind == KindHealth }

// List lists a project's monitors, oldest first. Every member of the
// workspace may.
func (m *Module) List(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[Monitor], error) {
	if err := m.authorize(ctx, userID, projectID, false); err != nil {
		return workspaces.Page[Monitor]{}, err
	}
	limit := min(max(p.Limit, 1), workspaces.MaxLimit)
	var after any
	if p.After != uuid.Nil {
		after = p.After
	}
	rows, err := m.db.Query(ctx, `
		SELECT `+columns+` FROM monitors
		WHERE project_id = $1 AND ($2::uuid IS NULL OR id > $2)
		ORDER BY id LIMIT $3`, projectID, after, limit+1)
	if err != nil {
		return workspaces.Page[Monitor]{}, fmt.Errorf("list monitors: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Monitor, error) { return read(row) })
	if err != nil {
		return workspaces.Page[Monitor]{}, fmt.Errorf("list monitors: %w", err)
	}
	if len(items) > limit {
		return workspaces.Page[Monitor]{Items: items[:limit], More: true}, nil
	}
	return workspaces.Page[Monitor]{Items: items}, nil
}

// Get returns a monitor. Every member of the workspace may read it.
func (m *Module) Get(ctx context.Context, userID, monitorID uuid.UUID) (Monitor, error) {
	mon, err := read(m.db.QueryRow(ctx, `SELECT `+columns+` FROM monitors WHERE id = $1`, monitorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("get a monitor: %w", err)
	}
	if err := m.authorize(ctx, userID, mon.ProjectID, false); err != nil {
		return Monitor{}, err
	}
	return mon, nil
}

// Update changes a monitor. Owners, admins and editors may. Changing what it
// checks forgets failures so far, since they were failures of something
// else. Pausing stops its checks; resuming starts it again as pending.
func (m *Module) Update(ctx context.Context, userID, monitorID uuid.UUID, pre workspaces.Precondition, patch Patch) (Monitor, error) {
	if _, err := m.Get(ctx, userID, monitorID); err != nil {
		return Monitor{}, err
	}
	var updated Monitor
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		cur, err := read(tx.QueryRow(ctx, `SELECT `+columns+` FROM monitors WHERE id = $1 FOR UPDATE`, monitorID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := m.authorize(ctx, userID, cur.ProjectID, true); err != nil {
			return err
		}
		if !pre.Matches(cur.Version) {
			return ErrVersionMismatch
		}
		wasInterval, wasGrace, wasPaused, wasKind := cur.Interval, cur.Grace, cur.State == StatePaused, cur.Kind
		apply(&cur, patch)
		if cur.Kind != wasKind && (!sendsRequest(cur.Kind) || !sendsRequest(wasKind)) {
			var errs validate.Errors
			errs.Add("/kind", "kind", "A monitor's kind changes only between http and health. Create a new monitor instead.")
			return errs.Err()
		}
		if patch.Paused != nil {
			switch {
			case *patch.Paused:
				cur.State = StatePaused
			case wasPaused:
				cur.State = StatePending
			}
		}
		var errs validate.Errors
		checkValues(&errs, &cur)
		if err := errs.Err(); err != nil {
			return err
		}
		if err := m.checkPlace(ctx, userID, &cur); err != nil {
			return err
		}
		request, err := json.Marshal(cur.Request)
		if err != nil {
			return err
		}
		assertions, err := json.Marshal(cur.Assertions)
		if err != nil {
			return err
		}
		next := cur.NextCheckAt
		resumed := wasPaused && cur.State != StatePaused
		switch {
		case cur.Kind == KindHeartbeat && (resumed || cur.Interval != wasInterval || cur.Grace != wasGrace):
			next = heartbeatDue(cur, m.now(), resumed)
		case cur.Kind != KindHeartbeat && (cur.Interval != wasInterval || resumed):
			next = firstCheck(cur.ID, cur.Interval, m.now())
		}
		failures := cur.ConsecutiveFailures
		if patch.Request != nil || patch.Assertions != nil || patch.Kind != nil || patch.CollectionID != nil || patch.FolderID != nil || cur.State == StatePending {
			failures = 0
		}
		updated, err = read(tx.QueryRow(ctx, `
			UPDATE monitors SET service_id = $2, name = $3, kind = $4, request = $5, assertions = $6, interval_seconds = $7, timeout_seconds = $8,
				failures_to_confirm = $9, certificate_warning_days = $10, state = $11, consecutive_failures = $12, next_check_at = $13,
				confirm_at = CASE WHEN $12 = 0 THEN NULL ELSE confirm_at END, collection_id = $14, folder_id = $15, grace_seconds = $16,
				version = version + 1, updated_at = now()
			WHERE id = $1 RETURNING `+columns,
			monitorID, nullableID(cur.ServiceID), cur.Name, cur.Kind, request, assertions, int(cur.Interval/time.Second), int(cur.Timeout/time.Second),
			cur.FailuresToConfirm, cur.CertificateWarningDays, cur.State, failures, next,
			nullableID(cur.CollectionID), nullableID(cur.FolderID), int(cur.Grace/time.Second)))
		return err
	})
	var fields validate.Errors
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrVersionMismatch) || errors.As(err, &fields) {
		return Monitor{}, err
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("update a monitor: %w", err)
	}
	return updated, nil
}

func apply(cur *Monitor, p Patch) {
	if p.Name != nil {
		cur.Name = *p.Name
	}
	if p.Kind != nil {
		cur.Kind = *p.Kind
	}
	if p.ServiceID != nil {
		cur.ServiceID = *p.ServiceID
	}
	if p.Request != nil {
		cur.Request = *p.Request
	}
	if p.Assertions != nil {
		cur.Assertions = *p.Assertions
	}
	if p.CollectionID != nil && *p.CollectionID != cur.CollectionID {
		// A folder is its collection's.
		cur.CollectionID, cur.FolderID = *p.CollectionID, uuid.Nil
	}
	if p.FolderID != nil {
		cur.FolderID = *p.FolderID
	}
	if p.Grace != nil {
		cur.Grace = *p.Grace
	}
	if p.Interval != nil {
		cur.Interval = *p.Interval
	}
	if p.Timeout != nil {
		cur.Timeout = *p.Timeout
	}
	if p.FailuresToConfirm != nil {
		cur.FailuresToConfirm = *p.FailuresToConfirm
	}
	if p.CertificateWarningDays != nil {
		cur.CertificateWarningDays = *p.CertificateWarningDays
	}
}

// Delete removes a monitor. Owners, admins and editors may. Its results stay
// until they expire.
func (m *Module) Delete(ctx context.Context, userID, monitorID uuid.UUID, pre workspaces.Precondition) error {
	mon, err := m.Get(ctx, userID, monitorID)
	if err != nil {
		return err
	}
	if err := m.authorize(ctx, userID, mon.ProjectID, true); err != nil {
		return err
	}
	tag, err := m.db.Exec(ctx, `DELETE FROM monitors WHERE id = $1 AND ($2::bigint[] IS NULL OR version = ANY($2))`, monitorID, pre.Versions)
	if err != nil {
		return fmt.Errorf("delete a monitor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionMismatch
	}
	m.logger.InfoContext(ctx, "deleted a monitor", id.LogAttr("monitor_id", id.Monitor, monitorID), id.LogAttr("user_id", id.User, userID))
	return nil
}

// ServiceHealth is the health of a service in one environment, from its
// monitors.
type ServiceHealth struct {
	// State is the worst of its monitors', or "" when none has been checked.
	State string
	// Detail names what isn't healthy, for a tooltip.
	Detail string
}

// EnvironmentHealth returns the health of the services with monitors in an
// environment, for the canvas. The caller has checked access.
func (m *Module) EnvironmentHealth(ctx context.Context, environmentID uuid.UUID) (map[uuid.UUID]ServiceHealth, error) {
	rows, err := m.db.Query(ctx, `
		SELECT service_id, state, name, last_components FROM monitors
		WHERE environment_id = $1 AND service_id IS NOT NULL AND state NOT IN ('paused', 'pending')
		ORDER BY id`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("read services' health: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]ServiceHealth{}
	for rows.Next() {
		var (
			service     uuid.UUID
			state, name string
			components  []byte
		)
		if err := rows.Scan(&service, &state, &name, &components); err != nil {
			return nil, fmt.Errorf("read services' health: %w", err)
		}
		var parts []Component
		if len(components) > 0 {
			if err := json.Unmarshal(components, &parts); err != nil {
				return nil, fmt.Errorf("read services' health: %w", err)
			}
		}
		h := out[service]
		if severity(state) > severity(h.State) {
			h.State = state
		}
		if detail := unhealthy(name, state, parts); detail != "" {
			if h.Detail != "" {
				h.Detail += "; "
			}
			h.Detail += detail
		}
		out[service] = h
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read services' health: %w", err)
	}
	return out, nil
}

func severity(state string) int {
	switch state {
	case StateDown:
		return 3
	case StateDegraded:
		return 2
	case StateUp:
		return 1
	}
	return 0
}

// unhealthy says what isn't well about a monitor, or "" when all is.
func unhealthy(name, state string, parts []Component) string {
	if state == StateUp {
		return ""
	}
	var bad []string
	for _, c := range parts {
		if c.Status != ComponentOK {
			bad = append(bad, c.Name+" "+c.Status)
		}
	}
	if len(bad) == 0 {
		return name + " " + state
	}
	return name + " " + state + ": " + strings.Join(bad, ", ")
}
