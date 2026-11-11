// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/token"
)

// FailureNoHeartbeat is a heartbeat's failure: no call came in time.
const FailureNoHeartbeat = "no-heartbeat"

// Heartbeat records a call from a heartbeat monitor's job: the monitor is
// up, and the next call is due within its interval and grace. A paused
// monitor takes the call and changes nothing. ErrNotFound means no monitor
// has the token.
func (m *Module) Heartbeat(ctx context.Context, secret string) error {
	if !WellFormedHeartbeatToken(secret) {
		return ErrNotFound
	}
	var (
		check         Check
		before, after string
	)
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		mon, err := read(tx.QueryRow(ctx, `SELECT `+columns+` FROM monitors WHERE heartbeat_token_hash = $1 FOR UPDATE`, token.Hash(secret)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		before, after = mon.State, mon.State
		if mon.State == StatePaused {
			return nil
		}
		now := m.now()
		after, _, _ = transition(mon.State, mon.ConsecutiveFailures, 1, OutcomePassed)
		check = Check{MonitorID: mon.ID, EnvironmentID: mon.EnvironmentID, At: now, Outcome: OutcomePassed}
		mon.LastPingAt = now
		_, err = tx.Exec(ctx, `
			UPDATE monitors SET state = $2, consecutive_failures = 0, confirm_at = NULL, last_ping_at = $3, next_check_at = $4,
				last_check_at = $3, last_outcome = $5, last_status = NULL, last_duration_ms = 0, last_failure_code = NULL,
				last_failure_message = NULL, last_trace_id = NULL, last_components = NULL, last_run_id = NULL
			WHERE id = $1`, mon.ID, after, now, heartbeatDue(mon, now, false), OutcomePassed)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("record a heartbeat: %w", err)
	}
	if check.MonitorID != uuid.Nil {
		m.record(check)
		m.changed(ctx, check.MonitorID, before, after)
	}
	return nil
}

// WellFormedHeartbeatToken says whether a string could be a heartbeat
// token, without looking it up.
func WellFormedHeartbeatToken(secret string) bool {
	rest, ok := strings.CutPrefix(secret, HeartbeatTokenPrefix)
	return ok && token.WellFormed(rest)
}

// expire records a heartbeat that was due and didn't come. A call that came
// since it was claimed moved its time on, and wins.
func (m *Module) expire(ctx context.Context, c claimed) error {
	mon := c.monitor
	window := mon.Interval + mon.Grace
	var (
		check         Check
		before, after string
	)
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		var (
			state    string
			lastPing *time.Time
		)
		err := tx.QueryRow(ctx, `SELECT state, last_ping_at FROM monitors WHERE id = $1 FOR UPDATE`, mon.ID).Scan(&state, &lastPing)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state == StatePaused || lastPing != nil && lastPing.After(c.scheduled.Add(-window)) {
			_, err := tx.Exec(ctx, `UPDATE monitors SET running_since = NULL WHERE id = $1`, mon.ID)
			return err
		}
		check = Check{
			MonitorID: mon.ID, EnvironmentID: mon.EnvironmentID, At: m.now(), Outcome: OutcomeFailed,
			FailureCode: FailureNoHeartbeat, FailureMessage: missed(window, lastPing),
		}
		before, after, err = m.keep(ctx, tx, mon.ID, check, true)
		return err
	})
	if err != nil {
		return fmt.Errorf("record a missed heartbeat: %w", err)
	}
	if check.MonitorID != uuid.Nil {
		m.record(check)
		m.changed(ctx, mon.ID, before, after)
	}
	return nil
}

func missed(window time.Duration, lastPing *time.Time) string {
	if lastPing == nil {
		return fmt.Sprintf("No call came within %s, and none has come yet.", words(window))
	}
	return fmt.Sprintf("No call came within %s. The last came at %s.", words(window), lastPing.UTC().Format(time.RFC3339))
}

// words says a duration the way people do: 1 day 2 hours, 90 seconds.
func words(d time.Duration) string {
	units := []struct {
		size time.Duration
		name string
	}{{24 * time.Hour, "day"}, {time.Hour, "hour"}, {time.Minute, "minute"}, {time.Second, "second"}}
	var parts []string
	for _, u := range units {
		n := d / u.size
		if n == 0 {
			continue
		}
		d -= n * u.size
		name := u.name
		if n != 1 {
			name += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, name))
	}
	if len(parts) == 0 {
		return "0 seconds"
	}
	return strings.Join(parts, " ")
}

// RotateHeartbeatToken gives a heartbeat monitor a new token, and returns
// it. The old one stops working at once. Owners, admins and editors may.
func (m *Module) RotateHeartbeatToken(ctx context.Context, userID, monitorID uuid.UUID) (string, error) {
	mon, err := m.Get(ctx, userID, monitorID)
	if err != nil {
		return "", err
	}
	if err := m.authorize(ctx, userID, mon.ProjectID, true); err != nil {
		return "", err
	}
	if mon.Kind != KindHeartbeat {
		return "", ErrNoHeartbeat
	}
	secret := HeartbeatTokenPrefix + token.Random()
	tag, err := m.db.Exec(ctx, `
		UPDATE monitors SET heartbeat_token_hash = $2, version = version + 1, updated_at = now() WHERE id = $1 AND kind = 'heartbeat'`,
		monitorID, token.Hash(secret))
	if err != nil {
		return "", fmt.Errorf("rotate a heartbeat token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	m.logger.InfoContext(ctx, "rotated a heartbeat token", id.LogAttr("monitor_id", id.Monitor, monitorID), id.LogAttr("user_id", id.User, userID))
	return secret, nil
}
