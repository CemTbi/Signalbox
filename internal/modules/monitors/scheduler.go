// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

const (
	// concurrentChecks bounds the checks running at once per process. A
	// thousand one-minute monitors are 17 checks a second, so this leaves
	// room for most of them to take their whole timeout.
	concurrentChecks = 200
	// tickEvery is how often the scheduler asks what's due.
	tickEvery = time.Second
	// confirmAfter is how long after a failure its confirmation runs.
	confirmAfter = 10 * time.Second
	// runningGrace is how long past its timeout a check counts as running,
	// in case its process stopped without saying so.
	runningGrace = 30 * time.Second
)

// Schedule starts checks as they come due, until ctx ends. Every process
// with the scheduler role runs it: a monitor is claimed in PostgreSQL, and
// moved on to its next time in the same statement, so several schedulers
// never start the same check. It returns when checks in progress have ended.
func (m *Module) Schedule(ctx context.Context) {
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	defer m.running.Wait()
	for {
		if _, err := m.Tick(ctx); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "couldn't start monitors' checks; it will try again", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Tick starts the checks that are due and have a free slot, and says how
// many it started. They run in the background.
func (m *Module) Tick(ctx context.Context) (int, error) {
	started := 0
	for {
		free := cap(m.slots) - len(m.slots)
		if free == 0 {
			return started, nil
		}
		claims, err := m.claim(ctx, free)
		if err != nil {
			return started, err
		}
		for _, c := range claims {
			m.startClaimed(ctx, c)
		}
		started += len(claims)
		if len(claims) < free {
			return started, nil
		}
	}
}

// claim is a monitor whose time has come.
type claimed struct {
	monitor Monitor
	// scheduled is when its check should have started.
	scheduled time.Time
	// busy means its last check is still running.
	busy bool
}

// claim takes monitors that are due, moving each on to its next time at the
// same phase. Times missed while nothing ran are skipped, not caught up. A
// due confirmation doesn't move the schedule.
func (m *Module) claim(ctx context.Context, limit int) ([]claimed, error) {
	rows, err := m.db.Query(ctx, `
		WITH due AS (
			SELECT id,
			       CASE WHEN confirm_at <= now() THEN confirm_at ELSE next_check_at END AS scheduled,
			       running_since IS NOT NULL AND running_since > now() - make_interval(secs => timeout_seconds + $2::int) AS busy
			FROM monitors
			WHERE state <> 'paused' AND (next_check_at <= now() OR confirm_at <= now())
			ORDER BY least(next_check_at, coalesce(confirm_at, next_check_at))
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE monitors m SET
			next_check_at = CASE WHEN m.next_check_at <= now()
				THEN m.next_check_at + make_interval(secs => m.interval_seconds * (floor(extract(epoch FROM now() - m.next_check_at) / m.interval_seconds) + 1))
				ELSE m.next_check_at END,
			confirm_at = CASE WHEN m.confirm_at <= now() THEN NULL ELSE m.confirm_at END,
			running_since = CASE WHEN due.busy THEN m.running_since ELSE now() END
		FROM due WHERE m.id = due.id
		RETURNING m.`+columns+`, due.scheduled, due.busy`, limit, int(runningGrace/time.Second))
	if err != nil {
		return nil, fmt.Errorf("claim due monitors: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claimed, error) {
		var c claimed
		mon, err := read(row, &c.scheduled, &c.busy)
		c.monitor = mon
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("claim due monitors: %w", err)
	}
	return out, nil
}

func (m *Module) startClaimed(ctx context.Context, c claimed) {
	now := m.now()
	if c.busy {
		check := Check{
			MonitorID: c.monitor.ID, EnvironmentID: c.monitor.EnvironmentID, At: now, Outcome: OutcomeSkipped, FailureCode: "still-running",
			FailureMessage: "The check before this one was still running, so this one was skipped.",
		}
		m.record(check)
		return
	}
	late := now.Sub(c.scheduled)
	m.delay.Observe(late.Seconds())
	if m.observeDelay != nil {
		m.observeDelay(late)
	}
	// A free slot was counted before claiming; only this goroutine fills
	// them, apart from Check now, which doesn't use one.
	m.slots <- struct{}{}
	m.running.Add(1)
	go func() {
		defer m.running.Done()
		defer func() { <-m.slots }()
		if err := m.checkAndKeep(ctx, c.monitor); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "couldn't finish a monitor's check", "error", err, id.LogAttr("monitor_id", id.Monitor, c.monitor.ID))
		}
	}()
}

// checkAndKeep runs a monitor's check and records what it found.
func (m *Module) checkAndKeep(ctx context.Context, mon Monitor) error {
	check, err := m.perform(ctx, mon)
	// However it ended, the check isn't running any more, even if this
	// process is stopping.
	done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		if _, e := m.db.Exec(done, `UPDATE monitors SET running_since = NULL WHERE id = $1`, mon.ID); e != nil {
			return errors.Join(err, e)
		}
		if ctx.Err() != nil {
			return err // stopping, so nothing was learned about the target
		}
		m.record(Check{MonitorID: mon.ID, EnvironmentID: mon.EnvironmentID, At: m.now(), Outcome: OutcomeSkipped, FailureCode: "internal",
			FailureMessage: "Signalbox couldn't send this check, so it says nothing about the service."})
		return err
	}
	if ctx.Err() != nil {
		// The runner's failure would be this process stopping, not the
		// target failing.
		_, e := m.db.Exec(done, `UPDATE monitors SET running_since = NULL WHERE id = $1`, mon.ID)
		return e
	}
	m.record(check)
	return m.finish(done, mon.ID, check, true)
}

// perform sends the monitor's request and judges the response. An error
// means Signalbox couldn't try, which says nothing about the target.
func (m *Module) perform(ctx context.Context, mon Monitor) (Check, error) {
	env, err := m.access.Environment(ctx, mon.EnvironmentID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return Check{}, fmt.Errorf("the monitor's environment is gone: %w", err)
	}
	if err != nil {
		return Check{}, fmt.Errorf("read the environment: %w", err)
	}
	entry, err := m.prober.Probe(ctx, env, mon.Request, mon.Timeout)
	if err != nil {
		return Check{}, fmt.Errorf("send the check: %w", err)
	}
	return evaluate(&mon, entry, m.now()), nil
}

// finish moves the monitor's state on from a check's outcome, taking its
// current state under a lock: someone may have paused or changed it while
// the check ran. scheduled says the check is the one running_since marks: a
// check by hand has no mark, and mustn't clear a scheduled check's.
func (m *Module) finish(ctx context.Context, monitorID uuid.UUID, c Check, scheduled bool) error {
	var components []byte
	if len(c.Components) > 0 {
		var err error
		if components, err = json.Marshal(c.Components); err != nil {
			return fmt.Errorf("encode components: %w", err)
		}
	}
	var certificate *time.Time
	if !c.CertificateExpiresAt.IsZero() {
		certificate = &c.CertificateExpiresAt
	}
	var before, after string
	err := pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		var failures, threshold int
		err := tx.QueryRow(ctx, `SELECT state, consecutive_failures, failures_to_confirm FROM monitors WHERE id = $1 FOR UPDATE`, monitorID).
			Scan(&before, &failures, &threshold)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // deleted while it ran
		}
		if err != nil {
			return err
		}
		after = before
		var confirm *time.Time
		if before != StatePaused {
			var again bool
			after, failures, again = transition(before, failures, threshold, c.Outcome)
			if again {
				at := m.now().Add(confirmAfter)
				confirm = &at
			}
		}
		_, err = tx.Exec(ctx, `
			UPDATE monitors SET state = $2, consecutive_failures = $3, confirm_at = $4,
				running_since = CASE WHEN $14::boolean THEN NULL ELSE running_since END,
				last_check_at = $5, last_outcome = $6, last_status = $7, last_duration_ms = $8, last_failure_code = $9, last_failure_message = $10,
				last_trace_id = $11, last_components = $12, certificate_expires_at = coalesce($13, certificate_expires_at)
			WHERE id = $1`,
			monitorID, after, failures, confirm, c.At, c.Outcome, c.Status, float64(c.Duration)/float64(time.Millisecond), c.FailureCode, c.FailureMessage,
			c.TraceID, components, certificate, scheduled)
		return err
	})
	if err != nil {
		return fmt.Errorf("keep a check's result: %w", err)
	}
	if before != after {
		m.logger.InfoContext(ctx, "a monitor changed state", id.LogAttr("monitor_id", id.Monitor, monitorID), "from", before, "to", after)
	}
	return nil
}

// CheckNow runs a monitor's check at once and returns what it found.
// Owners, admins and editors may: it sends a request, as sending one by hand
// does. The schedule stays as it is.
func (m *Module) CheckNow(ctx context.Context, userID, monitorID uuid.UUID) (Check, error) {
	mon, err := m.Get(ctx, userID, monitorID)
	if err != nil {
		return Check{}, err
	}
	if err := m.authorize(ctx, userID, mon.ProjectID, true); err != nil {
		return Check{}, err
	}
	check, err := m.perform(ctx, mon)
	if err != nil {
		return Check{}, err
	}
	// Whoever asked has gone, so the runner's failure says nothing of the
	// service.
	if err := ctx.Err(); err != nil {
		return Check{}, err
	}
	m.record(check)
	if err := m.finish(ctx, mon.ID, check, false); err != nil {
		return Check{}, err
	}
	return check, nil
}
