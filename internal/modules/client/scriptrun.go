// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/scripts"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// Limits on what a send's scripts report.
const (
	maxScriptLogs    = 200
	maxScriptTests   = 1000
	maxScriptMessage = 10 << 10
	// scriptSlots bounds how many sends run scripts at once, since the
	// memory watchdog watches the whole process.
	scriptSlots = 4
)

// ScriptPool hands out script sessions; scripts.Pool is one.
type ScriptPool interface {
	Session() (*scripts.Session, error)
}

// UseScripts lets sends run pre-request and test scripts.
func (m *Module) UseScripts(s ScriptPool) {
	m.scripts = s
	m.scriptSlots = make(chan struct{}, scriptSlots)
}

// script is one script of a send, with where it comes from.
type script struct {
	// From names it: the collection's, a folder's or the request's name.
	From   string
	Source string
}

// chain is a send's scripts in the order they run: the collection's, then
// each folder's from the top, then the request's.
type chain struct {
	pre, test []script
	name      string
	itemID    uuid.UUID
}

func (c chain) empty() bool { return len(c.pre) == 0 && len(c.test) == 0 }

func (c *chain) add(from string, s Scripts) {
	if strings.TrimSpace(s.PreRequest) != "" {
		c.pre = append(c.pre, script{From: from, Source: s.PreRequest})
	}
	if strings.TrimSpace(s.Test) != "" {
		c.test = append(c.test, script{From: from, Source: s.Test})
	}
}

// scriptChain gathers the scripts of a saved request's collection and
// folders, and the request's own: own, when the editor sends them, or the
// saved ones. It returns the step to send, without its request.
func (m *Module) scriptChain(ctx context.Context, itemID uuid.UUID, own *Scripts) (Step, uuid.UUID, error) {
	c := Step{Name: "Request", ItemID: itemID}
	var collectionID uuid.UUID
	if itemID != uuid.Nil {
		var cs Collection
		rows, err := m.db.Query(ctx, `
			WITH RECURSIVE up AS (
				SELECT id, parent_id, collection_id, kind, name, scripts, request->'contract' AS contract, 0 AS depth FROM collection_items WHERE id = $1
				UNION ALL
				SELECT i.id, i.parent_id, i.collection_id, i.kind, i.name, i.scripts, NULL, up.depth + 1
				FROM collection_items i JOIN up ON i.id = up.parent_id
			)
			SELECT collection_id, kind, name, scripts, contract FROM up ORDER BY depth DESC`, itemID)
		if err != nil {
			return Step{}, uuid.Nil, err
		}
		type link struct {
			collection uuid.UUID
			kind, name string
			scripts    Scripts
			contract   *ContractRef
		}
		links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (link, error) {
			var l link
			err := row.Scan(&l.collection, &l.kind, &l.name, &l.scripts, &l.contract)
			return l, err
		})
		if err != nil {
			return Step{}, uuid.Nil, err
		}
		if len(links) == 0 || links[len(links)-1].kind != KindRequest {
			return Step{}, uuid.Nil, ErrNotFound
		}
		collectionID = links[0].collection
		if cs, err = readCollection(m.db.QueryRow(ctx, `SELECT `+collectionColumns+` FROM collections WHERE id = $1`, collectionID)); err != nil {
			return Step{}, uuid.Nil, err
		}
		c.Chain = append(c.Chain, Link{From: cs.Name, Scripts: cs.Scripts})
		var path []string
		for _, l := range links[:len(links)-1] {
			c.Chain = append(c.Chain, Link{From: l.name, Scripts: l.scripts})
			path = append(path, l.name)
		}
		last := links[len(links)-1]
		c.Name, c.Path, c.Contract = last.name, strings.Join(path, " / "), last.contract
		if own == nil {
			own = &last.scripts
		}
	}
	if own != nil {
		c.Chain = append(c.Chain, Link{From: c.Name, Scripts: *own})
	}
	return c, collectionID, nil
}

// ScriptTest is a test a script ran.
type ScriptTest struct {
	From    string `json:"from"`
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ScriptLog is a console call.
type ScriptLog struct {
	From    string `json:"from"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ScriptError is an error that ended a script.
type ScriptError struct {
	From    string `json:"from"`
	Event   string `json:"event"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// VariableChange is a variable a script set, unset or cleared. Changes
// last for the send; they aren't saved.
type VariableChange struct {
	From  string `json:"from"`
	Scope string `json:"scope"`
	Op    string `json:"op"`
	Key   string `json:"key,omitempty"`
}

// ScriptOutcome is what a send's scripts did.
type ScriptOutcome struct {
	Tests   []ScriptTest     `json:"tests,omitempty"`
	Logs    []ScriptLog      `json:"logs,omitempty"`
	Errors  []ScriptError    `json:"errors,omitempty"`
	Changes []VariableChange `json:"changes,omitempty"`
	// Skipped says a pre-request script skipped the request.
	Skipped bool `json:"skipped,omitempty"`
}

func clip(s string) string {
	if len(s) <= maxScriptMessage {
		return s
	}
	n := maxScriptMessage
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func (o *ScriptOutcome) record(from, event string, res *scripts.Result) {
	for _, t := range res.Tests {
		if len(o.Tests) >= maxScriptTests {
			break
		}
		st := ScriptTest{From: from, Name: clip(t.Name), Passed: t.Passed, Skipped: t.Skipped}
		if t.Error != nil {
			st.Error = clip(t.Error.Error())
		}
		o.Tests = append(o.Tests, st)
	}
	for _, l := range res.Logs {
		if len(o.Logs) >= maxScriptLogs {
			break
		}
		o.Logs = append(o.Logs, ScriptLog{From: from, Level: l.Level, Message: clip(l.Message)})
	}
	for _, c := range res.Changes {
		o.Changes = append(o.Changes, VariableChange{From: from, Scope: c.Scope, Op: c.Op, Key: c.Key})
	}
	if res.Error != nil {
		o.Errors = append(o.Errors, ScriptError{From: from, Event: event, Name: res.Error.Name, Message: clip(res.Error.Message)})
	}
}

// mask hides secrets in what the outcome keeps.
func (o *ScriptOutcome) mask(m masker) *ScriptOutcome {
	if o == nil {
		return nil
	}
	out := *o
	out.Tests = make([]ScriptTest, len(o.Tests))
	for i, t := range o.Tests {
		t.Name, t.Error = m.string(t.Name), m.string(t.Error)
		out.Tests[i] = t
	}
	out.Logs = make([]ScriptLog, len(o.Logs))
	for i, l := range o.Logs {
		l.Message = m.string(l.Message)
		out.Logs[i] = l
	}
	out.Errors = make([]ScriptError, len(o.Errors))
	for i, e := range o.Errors {
		e.Message = m.string(e.Message)
		out.Errors[i] = e
	}
	return &out
}

// scriptRun runs a step's scripts in the run's session.
type scriptRun struct {
	e                     *Engine
	session               *scripts.Session
	chain                 chain
	vars                  scripts.Variables
	iteration, iterations int
	outcome               ScriptOutcome
	// next is what setNextRequest last named.
	next *string
}

// slot waits for one of the engine's script slots; release gives it back.
func (r *scriptRun) slot(ctx context.Context) (release func(), err error) {
	select {
	case r.e.slots <- struct{}{}:
		return func() { <-r.e.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *scriptRun) info(event string) scripts.Info {
	info := scripts.Info{EventName: event, Iteration: r.iteration, IterationCount: max(r.iterations, 1), RequestName: r.chain.name}
	if r.chain.itemID != uuid.Nil {
		info.RequestID = id.Format(id.Item, r.chain.itemID)
	}
	return info
}

// pre runs the pre-request scripts, which may change the request and the
// variables. It returns false when the request mustn't be sent: a script
// failed, or skipped it.
func (r *scriptRun) pre(ctx context.Context, req *runner.Request) (bool, error) {
	if len(r.chain.pre) == 0 {
		return true, nil
	}
	release, err := r.slot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	for _, s := range r.chain.pre {
		res, err := r.session.Run(ctx, scripts.Input{Event: "prerequest", Source: s.Source, Request: toScriptRequest(*req), Variables: r.vars, Info: r.info("prerequest")})
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			r.outcome.Errors = append(r.outcome.Errors, ScriptError{From: s.From, Event: "prerequest", Name: "Error", Message: runError(err)})
			return false, nil
		}
		r.outcome.record(s.From, "prerequest", res)
		r.vars = res.Variables
		if res.NextRequest != nil {
			r.next = res.NextRequest
		}
		if res.Request != nil {
			fromScriptRequest(req, *res.Request)
		}
		if res.Error != nil {
			return false, nil
		}
		if res.SkipRequest {
			r.outcome.Skipped = true
			return false, nil
		}
	}
	return true, nil
}

// test runs the test scripts against the response.
func (r *scriptRun) test(ctx context.Context, req runner.Request, resp *runner.Response, total int64) error {
	if len(r.chain.test) == 0 {
		return nil
	}
	release, err := r.slot(ctx)
	if err != nil {
		return err
	}
	defer release()
	sr := &scripts.Response{Code: resp.Status, Status: resp.StatusText, Body: string(resp.Body), ResponseTime: total}
	for _, h := range resp.Headers {
		sr.Header = append(sr.Header, scripts.Header{Key: h.Key, Value: h.Value})
	}
	for _, s := range r.chain.test {
		res, err := r.session.Run(ctx, scripts.Input{Event: "test", Source: s.Source, Request: toScriptRequest(req), Response: sr, Variables: r.vars, Info: r.info("test")})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.outcome.Errors = append(r.outcome.Errors, ScriptError{From: s.From, Event: "test", Name: "Error", Message: runError(err)})
			continue
		}
		r.outcome.record(s.From, "test", res)
		r.vars = res.Variables
		if res.NextRequest != nil {
			r.next = res.NextRequest
		}
	}
	return nil
}

func runError(err error) string {
	switch {
	case errors.Is(err, scripts.ErrTimeout):
		return "The script ran longer than its 30-second limit."
	case errors.Is(err, scripts.ErrMemory):
		return "The script used more memory than its limit."
	default:
		return "The script couldn't run: " + err.Error()
	}
}

// values flattens the variable scopes for {{name}}, narrowest last, so it
// wins: globals, collection, environment, data, local.
func (r *scriptRun) values() map[string]string {
	out := map[string]string{}
	for _, scope := range []map[string]any{r.vars.Globals, r.vars.Collection, r.vars.Environment, r.vars.Data, r.vars.Local} {
		for k, v := range scope {
			out[k] = scriptValue(v)
		}
	}
	return out
}

func scriptValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func anyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func toScriptRequest(req runner.Request) scripts.Request {
	out := scripts.Request{URL: req.URL, Method: req.Method}
	for _, h := range req.Headers {
		out.Header = append(out.Header, scripts.Header{Key: h.Key, Value: h.Value})
	}
	if len(req.Body) > 0 {
		out.Body = &scripts.Body{Mode: "raw", Raw: string(req.Body)}
	}
	return out
}

// fromScriptRequest applies what a pre-request script did to pm.request.
func fromScriptRequest(req *runner.Request, sr scripts.Request) {
	req.URL, req.Method = sr.URL, sr.Method
	req.Headers = make([]runner.Header, 0, len(sr.Header))
	for _, h := range sr.Header {
		req.Headers = append(req.Headers, runner.Header{Key: h.Key, Value: h.Value})
	}
	req.Body = nil
	b := sr.Body
	if b == nil {
		return
	}
	switch b.Mode {
	case "raw":
		req.Body = []byte(b.Raw)
	case "urlencoded":
		var pairs []string
		for _, p := range b.URLEncoded {
			if !p.Disabled {
				pairs = append(pairs, url.QueryEscape(p.Key)+"="+url.QueryEscape(p.Value))
			}
		}
		req.Body = []byte(strings.Join(pairs, "&"))
		setHeader(req, "Content-Type", "application/x-www-form-urlencoded")
	case "formdata":
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, p := range b.FormData {
			if !p.Disabled {
				_ = w.WriteField(p.Key, p.Value)
			}
		}
		_ = w.Close()
		req.Body = buf.Bytes()
		setHeader(req, "Content-Type", w.FormDataContentType())
	}
}

func setHeader(req *runner.Request, name, value string) {
	for _, h := range req.Headers {
		if strings.EqualFold(h.Key, name) {
			return
		}
	}
	req.Headers = append(req.Headers, runner.Header{Key: name, Value: value})
}

// ScriptSender handles pm.sendRequest through the runner, so it goes
// where the egress policy allows and nowhere else. Give it to the script
// pool.
func (m *Module) ScriptSender() scripts.Sender {
	return sender(m.runner)
}

// ScriptSender handles pm.sendRequest through the engine's runner. Give
// it to the script pool.
func (e *Engine) ScriptSender() scripts.Sender {
	return sender(e.runner)
}

func sender(fallback Runner) scripts.Sender {
	return func(ctx context.Context, sr scripts.Request) (*scripts.Response, error) {
		run := fallback
		if r, ok := ctx.Value(runnerKey{}).(Runner); ok && r != nil {
			run = r
		}
		if run == nil {
			return nil, ErrRunnerUnavailable
		}
		var req runner.Request
		fromScriptRequest(&req, sr)
		res := run.Run(ctx, req)
		if res.Failure != nil {
			return nil, errors.New(res.Failure.Message)
		}
		out := &scripts.Response{Code: res.Response.Status, Status: res.Response.StatusText, Body: string(res.Response.Body), ResponseTime: res.Timings.Total.Milliseconds()}
		for _, h := range res.Response.Headers {
			out.Header = append(out.Header, scripts.Header{Key: h.Key, Value: h.Value})
		}
		return out, nil
	}
}
