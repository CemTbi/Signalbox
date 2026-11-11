// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// ErrIncidentResolved means the incident is resolved, so it can't be
// acknowledged.
var ErrIncidentResolved = errors.New("monitors: incident resolved")

// An incident's states.
const (
	IncidentOpen     = "open"
	IncidentResolved = "resolved"
)

// How an incident was resolved.
const (
	ResolvedRecovered = "recovered"
	ResolvedManual    = "manual"
	ResolvedDeleted   = "deleted"
)

// What a timeline records.
const (
	EventOpened       = "opened"
	EventState        = "state"
	EventAcknowledged = "acknowledged"
	EventResolved     = "resolved"
	EventPaused       = "paused"
	EventResumed      = "resumed"
)

// maxTimeline bounds the events an incident's page reads: its newest.
const maxTimeline = 500

// Person is who did something; a zero ID is Signalbox, or an account that's
// gone.
type Person struct {
	ID   uuid.UUID
	Name string
}

// Incident is a monitor's time down: opened when it went down, resolved when
// it came back up or someone resolved it.
type Incident struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// MonitorID is uuid.Nil once the monitor is deleted; MonitorName stays.
	MonitorID     uuid.UUID
	EnvironmentID uuid.UUID
	MonitorName   string
	State         string
	OpenedAt      time.Time
	// First is the check that took the monitor down.
	First          Last
	AcknowledgedAt time.Time
	AcknowledgedBy Person
	ResolvedAt     time.Time
	ResolvedBy     Person
	Resolution     string
	Version        int64
}

// IncidentEvent is one line of an incident's timeline.
type IncidentEvent struct {
	At     time.Time
	Kind   string
	Actor  Person
	From   string
	To     string
	Detail string
}

// IncidentQuery selects a project's incidents.
type IncidentQuery struct {
	// State is open, resolved, or "" for both.
	State string
	// MonitorID is one monitor's, or uuid.Nil for all.
	MonitorID uuid.UUID
	Page      workspaces.PageRequest
}

const incidentColumns = `i.id, i.project_id, coalesce(i.monitor_id, '00000000-0000-0000-0000-000000000000'),
	coalesce(i.environment_id, '00000000-0000-0000-0000-000000000000'), i.monitor_name, i.state, i.opened_at,
	i.first_check_at, coalesce(i.first_status, 0), i.first_failure_code, i.first_failure_message, i.first_trace_id,
	coalesce(i.first_run_id, '00000000-0000-0000-0000-000000000000'),
	i.acknowledged_at, coalesce(i.acknowledged_by, '00000000-0000-0000-0000-000000000000'), coalesce(ua.name, ''),
	i.resolved_at, coalesce(i.resolved_by, '00000000-0000-0000-0000-000000000000'), coalesce(ur.name, ''),
	i.resolution, i.version`

const incidentsFrom = ` FROM incidents i
	LEFT JOIN users ua ON ua.id = i.acknowledged_by
	LEFT JOIN users ur ON ur.id = i.resolved_by`

func readIncident(row pgx.Row) (Incident, error) {
	var (
		in               Incident
		acked, resolved  *time.Time
		firstStatus      int
		firstAt          time.Time
		code, msg, trace string
		firstRun         uuid.UUID
	)
	err := row.Scan(&in.ID, &in.ProjectID, &in.MonitorID, &in.EnvironmentID, &in.MonitorName, &in.State, &in.OpenedAt,
		&firstAt, &firstStatus, &code, &msg, &trace, &firstRun,
		&acked, &in.AcknowledgedBy.ID, &in.AcknowledgedBy.Name, &resolved, &in.ResolvedBy.ID, &in.ResolvedBy.Name,
		&in.Resolution, &in.Version)
	if err != nil {
		return Incident{}, err
	}
	in.First = Last{At: firstAt, Outcome: OutcomeFailed, Status: firstStatus, FailureCode: code, FailureMessage: msg, TraceID: trace, RunID: firstRun}
	if acked != nil {
		in.AcknowledgedAt = *acked
	}
	if resolved != nil {
		in.ResolvedAt = *resolved
	}
	return in, nil
}

// addEvent writes a line of an incident's timeline.
func (m *Module) addEvent(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID, kind string, actor uuid.UUID, from, to, detail string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO incident_events (id, incident_id, at, kind, actor_id, from_state, to_state, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id.New(), incidentID, m.now(), kind, nullableID(actor), from, to, detail)
	return err
}

func (m *Module) openIncident(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID) (uuid.UUID, error) {
	var open uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM incidents WHERE monitor_id = $1 AND state = 'open'`, monitorID).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return open, err
}

// incidentStep keeps a monitor's incidents in step with a change of its
// state by a check, inside the transaction that made it, with the monitor's
// row locked. Going down opens an incident unless one is open or held off;
// coming up resolves it; any other change goes on its timeline.
func (m *Module) incidentStep(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, before, after string, c Check) error {
	if before == after || before == "" {
		return nil
	}
	open, err := m.openIncident(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	switch {
	case open == uuid.Nil && after == StateDown:
		var (
			projectID, environmentID uuid.UUID
			name                     string
			held                     bool
		)
		if err := tx.QueryRow(ctx, `SELECT project_id, environment_id, name, incidents_held FROM monitors WHERE id = $1`, monitorID).
			Scan(&projectID, &environmentID, &name, &held); err != nil {
			return err
		}
		if held {
			return nil
		}
		incident := id.New()
		var status *int
		if c.Status != 0 {
			status = &c.Status
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO incidents (id, project_id, monitor_id, environment_id, monitor_name, opened_at, first_check_at, first_status,
				first_failure_code, first_failure_message, first_trace_id, first_run_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			incident, projectID, monitorID, environmentID, name, m.now(), c.At, status, c.FailureCode, c.FailureMessage, c.TraceID, nullableID(c.RunID)); err != nil {
			return err
		}
		if err := m.addEvent(ctx, tx, incident, EventOpened, uuid.Nil, before, after, c.FailureMessage); err != nil {
			return err
		}
		m.incidentsOpened.Inc()
	case open != uuid.Nil && after == StateUp:
		if _, err := tx.Exec(ctx, `
			UPDATE incidents SET state = 'resolved', resolved_at = $2, resolution = 'recovered', version = version + 1 WHERE id = $1`,
			open, m.now()); err != nil {
			return err
		}
		if err := m.addEvent(ctx, tx, open, EventResolved, uuid.Nil, before, after, "The monitor is up again."); err != nil {
			return err
		}
	case open != uuid.Nil:
		if err := m.addEvent(ctx, tx, open, EventState, uuid.Nil, before, after, c.FailureMessage); err != nil {
			return err
		}
	}
	if after == StateUp {
		_, err := tx.Exec(ctx, `UPDATE monitors SET incidents_held = false WHERE id = $1 AND incidents_held`, monitorID)
		return err
	}
	return nil
}

// pausedStep puts a monitor's pausing or resuming on its open incident's
// timeline, with who did it.
func (m *Module) pausedStep(ctx context.Context, tx pgx.Tx, monitorID, userID uuid.UUID, paused bool) error {
	open, err := m.openIncident(ctx, tx, monitorID)
	if err != nil || open == uuid.Nil {
		return err
	}
	if paused {
		return m.addEvent(ctx, tx, open, EventPaused, userID, "", StatePaused, "Its checks are paused.")
	}
	return m.addEvent(ctx, tx, open, EventResumed, userID, StatePaused, StatePending, "Its checks are resumed.")
}

// ListIncidents lists a project's incidents, open first, then newest first.
// Every member of the workspace may.
func (m *Module) ListIncidents(ctx context.Context, userID, projectID uuid.UUID, q IncidentQuery) (workspaces.Page[Incident], error) {
	if err := m.authorize(ctx, userID, projectID, false); err != nil {
		return workspaces.Page[Incident]{}, err
	}
	limit := q.Page.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, workspaces.MaxLimit)
	var after, monitor any
	if q.Page.After != uuid.Nil {
		after = q.Page.After
	}
	if q.MonitorID != uuid.Nil {
		monitor = q.MonitorID
	}
	rows, err := m.db.Query(ctx, `SELECT `+incidentColumns+incidentsFrom+`
		WHERE i.project_id = $1 AND ($2 = '' OR i.state = $2) AND ($3::uuid IS NULL OR i.monitor_id = $3)
		  AND ($4::uuid IS NULL OR (i.state = 'open', i.id) < ((SELECT state = 'open' FROM incidents WHERE id = $4), $4))
		ORDER BY i.state = 'open' DESC, i.id DESC
		LIMIT $5`, projectID, q.State, monitor, after, limit+1)
	if err != nil {
		return workspaces.Page[Incident]{}, fmt.Errorf("list incidents: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Incident, error) { return readIncident(row) })
	if err != nil {
		return workspaces.Page[Incident]{}, fmt.Errorf("list incidents: %w", err)
	}
	if len(items) > limit {
		return workspaces.Page[Incident]{Items: items[:limit], More: true}, nil
	}
	return workspaces.Page[Incident]{Items: items}, nil
}

func (m *Module) incident(ctx context.Context, q pgx.Row) (Incident, error) {
	in, err := readIncident(q)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("get an incident: %w", err)
	}
	return in, nil
}

// GetIncident returns an incident and its timeline, oldest first: its
// newest 500 events. Every member of the workspace may.
func (m *Module) GetIncident(ctx context.Context, userID, incidentID uuid.UUID) (Incident, []IncidentEvent, error) {
	in, err := m.incident(ctx, m.db.QueryRow(ctx, `SELECT `+incidentColumns+incidentsFrom+` WHERE i.id = $1`, incidentID))
	if err != nil {
		return Incident{}, nil, err
	}
	if err := m.authorize(ctx, userID, in.ProjectID, false); err != nil {
		return Incident{}, nil, err
	}
	rows, err := m.db.Query(ctx, `
		SELECT e.at, e.kind, coalesce(e.actor_id, '00000000-0000-0000-0000-000000000000'), coalesce(u.name, ''), e.from_state, e.to_state, e.detail
		FROM incident_events e LEFT JOIN users u ON u.id = e.actor_id
		WHERE e.incident_id = $1 ORDER BY e.id DESC LIMIT $2`, incidentID, maxTimeline)
	if err != nil {
		return Incident{}, nil, fmt.Errorf("read an incident's timeline: %w", err)
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (IncidentEvent, error) {
		var e IncidentEvent
		err := row.Scan(&e.At, &e.Kind, &e.Actor.ID, &e.Actor.Name, &e.From, &e.To, &e.Detail)
		return e, err
	})
	if err != nil {
		return Incident{}, nil, fmt.Errorf("read an incident's timeline: %w", err)
	}
	slices.Reverse(events)
	return in, events, nil
}

// act changes an incident under a lock, for someone who may edit its
// project.
func (m *Module) act(ctx context.Context, userID, incidentID uuid.UUID, change func(tx pgx.Tx, in Incident) error) (Incident, error) {
	var out Incident
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		in, err := m.incident(ctx, tx.QueryRow(ctx, `SELECT `+incidentColumns+incidentsFrom+` WHERE i.id = $1 FOR UPDATE OF i`, incidentID))
		if err != nil {
			return err
		}
		if err := m.authorize(ctx, userID, in.ProjectID, true); err != nil {
			return err
		}
		if err := change(tx, in); err != nil {
			return err
		}
		out, err = m.incident(ctx, tx.QueryRow(ctx, `SELECT `+incidentColumns+incidentsFrom+` WHERE i.id = $1`, incidentID))
		return err
	})
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrIncidentResolved) {
		return Incident{}, fmt.Errorf("change an incident: %w", err)
	}
	return out, err
}

// Acknowledge says someone is on an open incident. Owners, admins and
// editors may. Acknowledging it again changes nothing.
func (m *Module) Acknowledge(ctx context.Context, userID, incidentID uuid.UUID) (Incident, error) {
	return m.act(ctx, userID, incidentID, func(tx pgx.Tx, in Incident) error {
		if in.State == IncidentResolved {
			return ErrIncidentResolved
		}
		if !in.AcknowledgedAt.IsZero() {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE incidents SET acknowledged_at = $2, acknowledged_by = $3, version = version + 1 WHERE id = $1`,
			in.ID, m.now(), userID); err != nil {
			return err
		}
		return m.addEvent(ctx, tx, in.ID, EventAcknowledged, userID, "", "", "")
	})
}

// Resolve resolves an incident by hand. Owners, admins and editors may. Its
// monitor opens no new incident until it has been up once, so a monitor
// still down doesn't open one at its next check. Resolving it again changes
// nothing.
func (m *Module) Resolve(ctx context.Context, userID, incidentID uuid.UUID) (Incident, error) {
	return m.act(ctx, userID, incidentID, func(tx pgx.Tx, in Incident) error {
		if in.State == IncidentResolved {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE incidents SET state = 'resolved', resolved_at = $2, resolved_by = $3, resolution = 'manual', version = version + 1 WHERE id = $1`,
			in.ID, m.now(), userID); err != nil {
			return err
		}
		if in.MonitorID != uuid.Nil {
			if _, err := tx.Exec(ctx, `UPDATE monitors SET incidents_held = state <> 'up' WHERE id = $1`, in.MonitorID); err != nil {
				return err
			}
		}
		return m.addEvent(ctx, tx, in.ID, EventResolved, userID, "", "", "Resolved by hand.")
	})
}

// openIncidents fills in the monitors' open incidents.
func (m *Module) openIncidents(ctx context.Context, mons []Monitor) error {
	if len(mons) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(mons))
	for i, mon := range mons {
		ids[i] = mon.ID
	}
	rows, err := m.db.Query(ctx, `SELECT monitor_id, id FROM incidents WHERE monitor_id = ANY($1) AND state = 'open'`, ids)
	if err != nil {
		return fmt.Errorf("read open incidents: %w", err)
	}
	defer rows.Close()
	open := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var monitor, incident uuid.UUID
		if err := rows.Scan(&monitor, &incident); err != nil {
			return fmt.Errorf("read open incidents: %w", err)
		}
		open[monitor] = incident
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read open incidents: %w", err)
	}
	for i := range mons {
		mons[i].OpenIncidentID = open[mons[i].ID]
	}
	return nil
}
