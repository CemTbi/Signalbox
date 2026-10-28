// SPDX-License-Identifier: Apache-2.0

// Package report holds a test run as reports show it, and writes it as
// JUnit XML, JSON or a self-contained HTML page. It touches no database,
// so the server and `signalbox test run` write the same files. See
// docs/adr/0033-collection-runs.md.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// Statuses of a run.
const (
	Queued      = "queued"
	Running     = "running"
	Passed      = "passed"
	Failed      = "failed"
	Canceled    = "canceled"
	Interrupted = "interrupted"
)

// Run is a test run with its steps. Values are masked.
type Run struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Environment string    `json:"environment,omitempty"`
	Status      string    `json:"status"`
	Iterations  int       `json:"iterations"`
	Stopped     string    `json:"stopped,omitempty"`
	Totals      Totals    `json:"totals"`
	StartedAt   time.Time `json:"startedAt"`
	// Duration is from start to finish, or so far.
	Duration time.Duration `json:"-"`
	Steps    []Step        `json:"steps"`
}

// Totals count a run's requests and tests.
type Totals struct {
	Requests       int `json:"requests"`
	FailedRequests int `json:"failedRequests"`
	Tests          int `json:"tests"`
	FailedTests    int `json:"failedTests"`
}

// Add counts a step.
func (t *Totals) Add(s Step) {
	t.Requests++
	if s.Failed {
		t.FailedRequests++
	}
	if s.Scripts != nil {
		for _, x := range s.Scripts.Tests {
			if x.Skipped {
				continue
			}
			t.Tests++
			if !x.Passed {
				t.FailedTests++
			}
		}
	}
}

// Step is one request of a run.
type Step struct {
	Seq       int    `json:"seq"`
	Iteration int    `json:"iteration"`
	ItemID    string `json:"itemId,omitempty"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	// Failed says the step counts against the run.
	Failed     bool                  `json:"failed"`
	Duration   time.Duration         `json:"-"`
	Headers    []runner.Header       `json:"headers,omitempty"`
	Unresolved []string              `json:"unresolved,omitempty"`
	Failure    *runner.Failure       `json:"failure,omitempty"`
	Response   *Response             `json:"response,omitempty"`
	Scripts    *client.ScriptOutcome `json:"scripts,omitempty"`
	Note       string                `json:"note,omitempty"`
}

// Response is what a step got back, without its body.
type Response struct {
	Status     int             `json:"status"`
	StatusText string          `json:"statusText"`
	Headers    []runner.Header `json:"headers,omitempty"`
	Size       int             `json:"size"`
	// Body is the start of the body, kept when the step failed.
	Body string `json:"body,omitempty"`
}

// MaxKeptBody is how much of a failed step's response body is kept.
const MaxKeptBody = 16 << 10

// FromResult turns an engine's step into a report's.
func FromResult(seq int, r client.StepResult, p client.Plan) Step {
	e := r.Entry
	ps := p.Steps[r.Index]
	s := Step{
		Seq: seq, Iteration: r.Iteration, Name: ps.Name, Path: ps.Path, Method: e.Method, URL: e.URL,
		Failed: r.Failed(), Duration: e.Result.Timings.Total, Headers: e.Headers, Unresolved: e.Unresolved,
		Failure: e.Result.Failure, Scripts: e.Scripts, Note: r.Note,
	}
	if ps.ItemID != uuid.Nil {
		s.ItemID = id.Format(id.Item, ps.ItemID)
	}
	if res := e.Result.Response; res != nil {
		s.Response = &Response{Status: res.Status, StatusText: res.StatusText, Headers: res.Headers, Size: len(res.Body)}
		if s.Failed {
			s.Response.Body = string(cut(res.Body, MaxKeptBody))
		}
	}
	return s
}

// cut keeps at most n bytes, on a character boundary.
func cut(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	for n > 0 && b[n]&0xC0 == 0x80 {
		n--
	}
	return b[:n]
}

// Status is the response's status code, or 0.
func (s Step) Status() int {
	if s.Response == nil {
		return 0
	}
	return s.Response.Status
}

// Formats of reports.
const (
	JUnit = "junit"
	JSON  = "json"
	HTML  = "html"
)

// ContentType is a format's media type, and Ext its file extension.
func ContentType(format string) string {
	switch format {
	case JUnit:
		return "application/xml; charset=utf-8"
	case HTML:
		return "text/html; charset=utf-8"
	default:
		return "application/json"
	}
}

// Ext is a format's file extension.
func Ext(format string) string {
	if format == JUnit {
		return "xml"
	}
	return format
}

// Write writes the run in a format.
func Write(w io.Writer, format string, r Run) error {
	switch format {
	case JUnit:
		return writeJUnit(w, r)
	case JSON:
		return writeJSON(w, r)
	case HTML:
		return writeHTML(w, r)
	default:
		return fmt.Errorf("report: unknown format %q", format)
	}
}

type jsonRun struct {
	Run
	DurationMs int64      `json:"durationMs"`
	Steps      []jsonStep `json:"steps"`
}

type jsonStep struct {
	Step
	DurationMs int64 `json:"durationMs"`
}

func writeJSON(w io.Writer, r Run) error {
	out := jsonRun{Run: r, DurationMs: r.Duration.Milliseconds(), Steps: make([]jsonStep, len(r.Steps))}
	for i, s := range r.Steps {
		out.Steps[i] = jsonStep{Step: s, DurationMs: s.Duration.Milliseconds()}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
