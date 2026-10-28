// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/agents"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Agents is what the API needs from the agents module.
type Agents interface {
	Connect(ctx context.Context, userID uuid.UUID, in agents.Input) (agents.Agent, error)
	List(ctx context.Context, userID uuid.UUID) ([]agents.Agent, error)
	Delete(ctx context.Context, userID, agentID uuid.UUID) error
	Next(ctx context.Context, userID, agentID uuid.UUID, wait time.Duration) (*agents.Job, error)
	Complete(ctx context.Context, userID, agentID, jobID uuid.UUID, res runner.Result) error
}

// resultLimit bounds a job's result: a 10 MiB body in base64, and headers.
const resultLimit = 16 << 20

// AgentJSON is an agent in API responses.
type AgentJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Hostname   string `json:"hostname,omitempty"`
	OS         string `json:"os,omitempty"`
	Version    string `json:"version,omitempty"`
	Online     bool   `json:"online"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
}

func agentJSON(a agents.Agent) AgentJSON {
	return AgentJSON{ID: id.Format(id.Agent, a.ID), Name: a.Name, Hostname: a.Hostname, OS: a.OS, Version: a.Version, Online: a.Online,
		CreatedAt: timestamp(a.CreatedAt), LastSeenAt: timestamp(a.LastSeenAt)}
}

// AgentJobJSON is a request an agent sends.
type AgentJobJSON struct {
	ID      string       `json:"id"`
	Method  string       `json:"method"`
	URL     string       `json:"url"`
	Headers []HeaderJSON `json:"headers"`
	// Body is base64.
	Body      string `json:"body,omitempty"`
	TimeoutMs int64  `json:"timeoutMs,omitempty"`
}

// AgentResultJSON is what an agent got for a job.
type AgentResultJSON struct {
	Response *AgentResponseJSON `json:"response,omitempty"`
	Failure  *FailureJSON       `json:"failure,omitempty"`
	Timings  TimingsJSON        `json:"timings"`
}

// AgentResponseJSON is a response an agent got. Its body is base64.
type AgentResponseJSON struct {
	Status     int          `json:"status"`
	StatusText string       `json:"statusText,omitempty"`
	Proto      string       `json:"proto,omitempty"`
	Headers    []HeaderJSON `json:"headers"`
	Body       string       `json:"body,omitempty"`
	URL        string       `json:"url,omitempty"`
	Redirects  int          `json:"redirects,omitempty"`
	RemoteAddr string       `json:"remoteAddress,omitempty"`
	Truncated  bool         `json:"truncated,omitempty"`
}

func (a *api) agentsError(w http.ResponseWriter, r *http.Request, err error) {
	var fields validate.Errors
	switch {
	case errors.As(err, &fields):
		writeInvalid(w, r, fields)
	case errors.Is(err, agents.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "not-found", "There is no agent or job with this ID. A deleted agent connects again on its own.")
	case errors.Is(err, context.Canceled):
		// The agent hung up while waiting.
	default:
		a.fail(w, r, "agent", err)
	}
}

func (a *api) listAgents(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	list, err := a.Agents.List(r.Context(), s.User.ID)
	if err != nil {
		a.agentsError(w, r, err)
		return
	}
	out := make([]AgentJSON, len(list))
	for i, ag := range list {
		out[i] = agentJSON(ag)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *api) connectAgent(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	var body struct {
		Name     *string `json:"name"`
		Hostname string  `json:"hostname"`
		OS       string  `json:"os"`
		Version  string  `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var fields validate.Errors
	name := required(&fields, "/name", body.Name)
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	ag, err := a.Agents.Connect(r.Context(), s.User.ID, agents.Input{Name: name, Hostname: body.Hostname, OS: body.OS, Version: body.Version})
	if err != nil {
		a.agentsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, agentJSON(ag))
}

func (a *api) deleteAgent(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	agentID, ok := pathID(w, r, "agentId", id.Agent, "agent")
	if !ok {
		return
	}
	if err := a.Agents.Delete(r.Context(), s.User.ID, agentID); err != nil {
		a.agentsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) nextAgentJob(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	agentID, ok := pathID(w, r, "agentId", id.Agent, "agent")
	if !ok {
		return
	}
	wait := agents.MaxWait
	if v := r.URL.Query().Get("waitMs"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms < 0 || ms > int(agents.MaxWait.Milliseconds()) {
			writeInvalid(w, r, validate.Errors{{Pointer: "/waitMs", Code: "range", Detail: "Wait from 0 to 25,000 milliseconds."}})
			return
		}
		wait = time.Duration(ms) * time.Millisecond
	}
	j, err := a.Agents.Next(r.Context(), s.User.ID, agentID, wait)
	if err != nil {
		a.agentsError(w, r, err)
		return
	}
	if j == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	out := AgentJobJSON{ID: id.Format(id.Job, j.ID), Method: j.Request.Method, URL: j.Request.URL, Headers: headersJSON(j.Request.Headers),
		TimeoutMs: j.Request.Timeout.Milliseconds()}
	if len(j.Request.Body) > 0 {
		out.Body = base64.StdEncoding.EncodeToString(j.Request.Body)
	}
	// It carries the owner's secrets, filled in.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (a *api) completeAgentJob(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	agentID, ok := pathID(w, r, "agentId", id.Agent, "agent")
	if !ok {
		return
	}
	jobID, ok := pathID(w, r, "jobId", id.Job, "job")
	if !ok {
		return
	}
	var body AgentResultJSON
	if !decodeJSONUpTo(w, r, &body, resultLimit) {
		return
	}
	res := runner.Result{Timings: timingsIn(body.Timings)}
	var fields validate.Errors
	switch {
	case body.Response != nil:
		in := body.Response
		raw, err := base64.StdEncoding.DecodeString(in.Body)
		if err != nil {
			fields.Add("/response/body", "format", "Send the body in base64.")
		}
		if in.Status < 100 || in.Status > 999 {
			fields.Add("/response/status", "range", "Send the status code, from 100 to 999.")
		}
		res.Response = &runner.Response{Status: in.Status, StatusText: in.StatusText, Proto: in.Proto, Body: raw, URL: in.URL,
			Redirects: in.Redirects, RemoteAddr: in.RemoteAddr, Truncated: in.Truncated}
		for _, h := range in.Headers {
			res.Response.Headers = append(res.Response.Headers, runner.Header(h))
		}
	case body.Failure != nil:
		res.Failure = &runner.Failure{Code: body.Failure.Code, Message: body.Failure.Message}
	default:
		fields.Add("/response", "required", "Send the response, or why there's none as failure.")
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return
	}
	if err := a.Agents.Complete(r.Context(), s.User.ID, agentID, jobID, res); err != nil {
		a.agentsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func timingsIn(t TimingsJSON) runner.Timings {
	d := func(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }
	return runner.Timings{DNS: d(t.DNS), Connect: d(t.Connect), TLS: d(t.TLS), FirstByte: d(t.FirstByte), Download: d(t.Download), Total: d(t.Total)}
}
