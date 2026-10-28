// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Client is what the API needs from the client module.
type Client interface {
	Send(ctx context.Context, userID, projectID uuid.UUID, req runner.Request, opts client.SendOptions) (client.Entry, error)
	History(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (client.HistoryPage, error)
	HistoryEntry(ctx context.Context, userID, entryID uuid.UUID) (client.Entry, error)
}

// sendBodyLimit bounds a send request: a 10 MiB body, quoted in JSON.
const sendBodyLimit = 24 << 20

// HeaderJSON is one header.
type HeaderJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// AuthJSON is how a request authenticates. In history, only its type,
// username and API key name are kept.
type AuthJSON struct {
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	In       string `json:"in,omitempty"`
}

func authJSON(a *client.Auth) *AuthJSON {
	if a == nil {
		return nil
	}
	out := AuthJSON(*a)
	return &out
}

func authIn(a *AuthJSON) *client.Auth {
	if a == nil {
		return nil
	}
	out := client.Auth(*a)
	return &out
}

// TimingsJSON are a request's phases, in milliseconds.
type TimingsJSON struct {
	DNS       float64 `json:"dnsMs"`
	Connect   float64 `json:"connectMs"`
	TLS       float64 `json:"tlsMs"`
	FirstByte float64 `json:"firstByteMs"`
	Download  float64 `json:"downloadMs"`
	Total     float64 `json:"totalMs"`
}

// SentRequestJSON is a request as history keeps it.
type SentRequestJSON struct {
	Auth          *AuthJSON    `json:"auth,omitempty"`
	Headers       []HeaderJSON `json:"headers"`
	Body          string       `json:"body,omitempty"`
	BodyTruncated bool         `json:"bodyTruncated,omitempty"`
}

// ResponseJSON is a response.
type ResponseJSON struct {
	Status     int          `json:"status"`
	StatusText string       `json:"statusText,omitempty"`
	Proto      string       `json:"proto"`
	Headers    []HeaderJSON `json:"headers"`
	// Body is text, or base64 when it isn't UTF-8.
	Body         string `json:"body"`
	BodyEncoding string `json:"bodyEncoding"`
	Size         int    `json:"size"`
	Truncated    bool   `json:"truncated"`
	URL          string `json:"url"`
	Redirects    int    `json:"redirects"`
	RemoteAddr   string `json:"remoteAddress,omitempty"`
}

// FailureJSON is why a request got no response.
type FailureJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HistoryEntryJSON is a sent request and what came of it.
type HistoryEntryJSON struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId,omitempty"`
	// Unresolved lists the {{names}} that had no value.
	Unresolved []string           `json:"unresolved,omitempty"`
	Method     string             `json:"method"`
	URL        string             `json:"url"`
	SentAt     string             `json:"sentAt"`
	Request    SentRequestJSON    `json:"request"`
	Response   *ResponseJSON      `json:"response,omitempty"`
	Scripts    *ScriptOutcomeJSON `json:"scripts,omitempty"`
	Failure    *FailureJSON       `json:"failure,omitempty"`
	Timings    TimingsJSON        `json:"timings"`
}

// ScriptOutcomeJSON is what a send's scripts did.
type ScriptOutcomeJSON struct {
	Tests   []client.ScriptTest     `json:"tests"`
	Logs    []client.ScriptLog      `json:"logs"`
	Errors  []client.ScriptError    `json:"errors"`
	Changes []client.VariableChange `json:"changes"`
	Skipped bool                    `json:"skipped"`
}

func scriptOutcomeJSON(o *client.ScriptOutcome) *ScriptOutcomeJSON {
	if o == nil {
		return nil
	}
	out := &ScriptOutcomeJSON{Tests: o.Tests, Logs: o.Logs, Errors: o.Errors, Changes: o.Changes, Skipped: o.Skipped}
	if out.Tests == nil {
		out.Tests = []client.ScriptTest{}
	}
	if out.Logs == nil {
		out.Logs = []client.ScriptLog{}
	}
	if out.Errors == nil {
		out.Errors = []client.ScriptError{}
	}
	if out.Changes == nil {
		out.Changes = []client.VariableChange{}
	}
	return out
}

func scriptsIn(s *ScriptsJSON) *client.Scripts {
	if s == nil {
		return nil
	}
	out := client.Scripts(*s)
	return &out
}

// HistorySummaryJSON is an entry in a history list.
type HistorySummaryJSON struct {
	ID         string  `json:"id"`
	Method     string  `json:"method"`
	URL        string  `json:"url"`
	Status     int     `json:"status,omitempty"`
	Failure    string  `json:"failure,omitempty"`
	DurationMs float64 `json:"durationMs"`
	SentAt     string  `json:"sentAt"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func headersJSON(hs []runner.Header) []HeaderJSON {
	out := make([]HeaderJSON, len(hs))
	for i, h := range hs {
		out[i] = HeaderJSON(h)
	}
	return out
}

func historyEntryJSON(e client.Entry) HistoryEntryJSON {
	t := e.Result.Timings
	out := HistoryEntryJSON{
		ID: id.Format(id.History, e.ID), Method: e.Method, URL: e.URL, SentAt: timestamp(e.SentAt),
		Request:    SentRequestJSON{Auth: authJSON(e.Auth), Headers: headersJSON(e.Headers), Body: string(e.Body), BodyTruncated: e.BodyTruncated},
		Timings:    TimingsJSON{ms(t.DNS), ms(t.Connect), ms(t.TLS), ms(t.FirstByte), ms(t.Download), ms(t.Total)},
		Unresolved: e.Unresolved,
	}
	if e.EnvironmentID != uuid.Nil {
		out.EnvironmentID = id.Format(id.Environment, e.EnvironmentID)
	}
	if f := e.Result.Failure; f != nil {
		out.Failure = &FailureJSON{Code: f.Code, Message: f.Message}
	}
	out.Scripts = scriptOutcomeJSON(e.Scripts)
	if r := e.Result.Response; r != nil {
		body, encoding := string(r.Body), "text"
		if !utf8.Valid(r.Body) {
			body, encoding = base64.StdEncoding.EncodeToString(r.Body), "base64"
		}
		out.Response = &ResponseJSON{
			Status: r.Status, StatusText: r.StatusText, Proto: r.Proto, Headers: headersJSON(r.Headers),
			Body: body, BodyEncoding: encoding, Size: len(r.Body), Truncated: r.Truncated,
			URL: r.URL, Redirects: r.Redirects, RemoteAddr: r.RemoteAddr,
		}
	}
	return out
}

// clientError answers for an error from the client module.
func (a *api) clientError(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, client.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no "+what+" with this ID.")
	case errors.Is(err, client.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "permission-denied",
			"Your role in this workspace doesn't allow sending requests. Ask a workspace owner or admin to make you an editor.")
	case errors.Is(err, client.ErrRunnerUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, "runner-unavailable",
			"This server doesn't run the runner role, which sends requests. Run signalbox-server with --roles=all.")
	default:
		a.fail(w, r, what, err)
	}
}

func (a *api) sendRequest(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	if ok, retry := a.sendsByUser.Allow(s.User.ID.String()); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	var body struct {
		EnvironmentID string       `json:"environmentId"`
		CollectionID  string       `json:"collectionId"`
		ItemID        string       `json:"itemId"`
		Scripts       *ScriptsJSON `json:"scripts"`
		Method        *string      `json:"method"`
		URL           *string      `json:"url"`
		Headers       []HeaderJSON `json:"headers"`
		Body          string       `json:"body"`
		Auth          *AuthJSON    `json:"auth"`
		TimeoutMs     int          `json:"timeoutMs"`
	}
	if !decodeJSONUpTo(w, r, &body, sendBodyLimit) {
		return
	}
	var fields validate.Errors
	req := runner.Request{
		Method: required(&fields, "/method", body.Method), URL: required(&fields, "/url", body.URL),
		Body: []byte(body.Body), Timeout: time.Duration(body.TimeoutMs) * time.Millisecond,
	}
	if len(req.Body) > 10<<20 {
		fields.Add("/body", "maxLength", "Send at most 10 MiB.")
	}
	var environmentID uuid.UUID
	if body.EnvironmentID != "" {
		var err error
		if environmentID, err = id.Parse(id.Environment, body.EnvironmentID); err != nil {
			fields.Add("/environmentId", "pattern", "Use an environment ID, such as env_01j9z3n5q7s9v1w3y5a7c9e1g3.")
		}
	}
	var itemID uuid.UUID
	if body.ItemID != "" {
		var err error
		if itemID, err = id.Parse(id.Item, body.ItemID); err != nil {
			fields.Add("/itemId", "pattern", "Use a saved request's ID, such as item_01j9z3w5y7a9c1e3g5j7m9p1r3.")
		}
	}
	var collectionID uuid.UUID
	if body.CollectionID != "" {
		var err error
		if collectionID, err = id.Parse(id.Collection, body.CollectionID); err != nil {
			fields.Add("/collectionId", "pattern", "Use a collection ID, such as col_01j9z3w5y7a9c1e3g5j7m9p1r3.")
		}
	}
	if body.TimeoutMs < 0 || body.TimeoutMs > 300_000 {
		fields.Add("/timeoutMs", "maximum", "Use a timeout from 0 to 300,000 ms.")
	}
	if len(body.Headers) > 200 {
		fields.Add("/headers", "maxItems", "Send at most 200 headers.")
	}
	// The sizes api/openapi.yaml promises, so one send can't fill history.
	if len(req.Method) > 32 {
		fields.Add("/method", "maxLength", "Use a method of at most 32 characters.")
	}
	if len(req.URL) > 8192 {
		fields.Add("/url", "maxLength", "Use a URL of at most 8,192 characters.")
	}
	for i, h := range body.Headers {
		if len(h.Key) > 1000 {
			fields.Add(fmt.Sprintf("/headers/%d/key", i), "maxLength", "Use a header name of at most 1,000 characters.")
		}
		if len(h.Value) > 65536 {
			fields.Add(fmt.Sprintf("/headers/%d/value", i), "maxLength", "Use a header value of at most 64 KiB.")
		}
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	for _, h := range body.Headers {
		req.Headers = append(req.Headers, runner.Header(h))
	}
	e, err := a.Client.Send(r.Context(), s.User.ID, projectID, req, client.SendOptions{
		EnvironmentID: environmentID, CollectionID: collectionID, ItemID: itemID, Scripts: scriptsIn(body.Scripts), Auth: authIn(body.Auth),
	})
	if err != nil {
		a.secretsError(w, r, "project or environment", err)
		return
	}
	w.Header().Set("Location", "/api/v1/history/"+id.Format(id.History, e.ID))
	writeJSON(w, http.StatusOK, historyEntryJSON(e))
}

func (a *api) listHistory(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	projectID, ok := pathID(w, r, "projectId", id.Project, "project")
	if !ok {
		return
	}
	req, ok := pageRequest(w, r, id.History)
	if !ok {
		return
	}
	page, err := a.Client.History(r.Context(), s.User.ID, projectID, req)
	if err != nil {
		a.clientError(w, r, "project", err)
		return
	}
	writeJSON(w, http.StatusOK, pageJSON(page, id.History, func(e client.Summary) uuid.UUID { return e.ID }, func(e client.Summary) HistorySummaryJSON {
		return HistorySummaryJSON{ID: id.Format(id.History, e.ID), Method: e.Method, URL: e.URL, Status: e.Status, Failure: e.Failure,
			DurationMs: ms(e.Duration), SentAt: timestamp(e.SentAt)}
	}))
}

func (a *api) getHistoryEntry(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	entryID, ok := pathID(w, r, "entryId", id.History, "history entry")
	if !ok {
		return
	}
	e, err := a.Client.HistoryEntry(r.Context(), s.User.ID, entryID)
	if err != nil {
		a.clientError(w, r, "history entry", err)
		return
	}
	writeJSON(w, http.StatusOK, historyEntryJSON(e))
}
