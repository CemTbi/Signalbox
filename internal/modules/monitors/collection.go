// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/testruns"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
)

// maxRunMessage bounds a failure message built from a run's steps.
const maxRunMessage = 1000

// runCollection runs a collection monitor's collection as a test run and
// judges it. An error means Signalbox couldn't run it.
func (m *Module) runCollection(ctx context.Context, mon Monitor, env workspaces.Environment) (Check, error) {
	if m.runs == nil {
		return Check{}, errors.New("this process can't run collections")
	}
	res, err := m.runs.RunForMonitor(ctx, testruns.MonitorRun{
		MonitorID: mon.ID, CollectionID: mon.CollectionID, FolderID: mon.FolderID, Environment: env, Limit: mon.Timeout,
	})
	if errors.Is(err, testruns.ErrNothingToRun) {
		return Check{
			MonitorID: mon.ID, EnvironmentID: mon.EnvironmentID, At: m.now(), Outcome: OutcomeSkipped, FailureCode: "nothing-to-run",
			FailureMessage: fmt.Sprintf("The collection has no requests to run, or more than %d, so this check says nothing about the service.", client.MaxRunSteps),
		}, nil
	}
	if err != nil {
		return Check{}, fmt.Errorf("run the collection: %w", err)
	}
	return evaluateRun(&mon, res, m.now()), nil
}

// evaluateRun decides a collection monitor's outcome from its run: it
// passes when the run passes. A failure names the first step that failed.
func evaluateRun(mon *Monitor, res testruns.MonitorResult, now time.Time) Check {
	r := res.Run
	c := Check{MonitorID: mon.ID, EnvironmentID: mon.EnvironmentID, At: now, Outcome: OutcomePassed, RunID: r.ID}
	if !r.StartedAt.IsZero() && r.FinishedAt.After(r.StartedAt) {
		c.Duration = r.FinishedAt.Sub(r.StartedAt)
	}
	switch r.Status {
	case testruns.StatusPassed:
		return c
	case testruns.StatusCanceled:
		c.Outcome, c.FailureCode = OutcomeSkipped, "canceled"
		c.FailureMessage = "Someone canceled the run, so this check says nothing about the service."
		return c
	}
	c.Outcome = OutcomeFailed
	if r.Stopped == testruns.StoppedTime {
		c.FailureCode = "timeout"
		c.FailureMessage = fmt.Sprintf("The run took longer than its timeout of %s.", words(mon.Timeout))
		return c
	}
	s := res.FirstFailed
	if s == nil {
		c.FailureCode, c.FailureMessage = "run", "The run failed."
		return c
	}
	c.TraceID = s.TraceID
	if s.Response != nil {
		c.Status = s.Response.Status
	}
	where := s.Name
	if s.Path != "" {
		where = s.Path + " / " + s.Name
	}
	switch {
	case s.Failure != nil:
		c.FailureCode, c.FailureMessage = s.Failure.Code, where+": "+s.Failure.Message
	case s.Scripts != nil && len(s.Scripts.Errors) > 0:
		c.FailureCode, c.FailureMessage = "script", where+": "+s.Scripts.Errors[0].Message
	default:
		c.FailureCode = "tests"
		c.FailureMessage = fmt.Sprintf("%d of %d tests failed", r.Totals.FailedTests, r.Totals.Tests)
		if t := firstFailedTest(s); t != nil {
			c.FailureMessage += fmt.Sprintf(`, first "%s" in %s`, t.Name, where)
			if t.Error != "" {
				c.FailureMessage += ": " + t.Error
			}
		}
		c.FailureMessage += "."
	}
	c.FailureMessage = clipMessage(c.FailureMessage)
	return c
}

func firstFailedTest(s *testruns.Step) *client.ScriptTest {
	if s.Scripts == nil {
		return nil
	}
	for i, t := range s.Scripts.Tests {
		if !t.Passed && !t.Skipped {
			return &s.Scripts.Tests[i]
		}
	}
	return nil
}

func clipMessage(s string) string {
	if len(s) <= maxRunMessage {
		return s
	}
	n := maxRunMessage
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
