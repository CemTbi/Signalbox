// SPDX-License-Identifier: Apache-2.0

// Package scripts runs Postman pre-request and test scripts in Sobek, a
// JavaScript engine written in Go. The sandbox offers what Postman's does:
// pm with pm.test and pm.expect, pm.request and pm.response, variable scopes
// and pm.sendRequest; the globals of Postman's older sandbox, such as tests
// and responseBody; and the libraries scripts require, such as lodash,
// moment and crypto-js. It has no file, process or network access of its
// own: requests go through the Send function the caller provides. See
// docs/adr/0017-postman-scripts.md.
package scripts

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	rtmetrics "runtime/metrics"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grafana/sobek"

	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

// sandboxJS is pm.js and the libraries, bundled by sandbox/build.mjs.
//
//go:embed sandbox/sandbox.js
var sandboxJS string

// ThirdPartyLicenses are the licenses of the libraries bundled into the
// sandbox, which must ship with it.
//
//go:embed sandbox/THIRD_PARTY_LICENSES.txt
var ThirdPartyLicenses string

var compileSandbox = sync.OnceValues(func() (*sobek.Program, error) {
	return sobek.Compile("sandbox.js", sandboxJS, true)
})

// Header is one HTTP header.
type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Body is a request body in Postman's format.
type Body struct {
	// Mode is raw, urlencoded or formdata.
	Mode       string  `json:"mode"`
	Raw        string  `json:"raw,omitempty"`
	URLEncoded []Param `json:"urlencoded,omitempty"`
	FormData   []Param `json:"formdata,omitempty"`
}

// Param is one field of a form body.
type Param struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Request is the request a script sees as pm.request, and can change in a
// pre-request script.
type Request struct {
	URL    string   `json:"url"`
	Method string   `json:"method"`
	Header []Header `json:"header,omitempty"`
	Body   *Body    `json:"body,omitempty"`
}

// Response is the response a test script sees as pm.response.
type Response struct {
	Code   int      `json:"code"`
	Status string   `json:"status,omitempty"`
	Header []Header `json:"header,omitempty"`
	Body   string   `json:"body"`
	// ResponseTime is in milliseconds, as Postman reports it.
	ResponseTime int64 `json:"responseTime"`
}

// Variables are the scopes a script reads and changes. Values are what
// JSON can hold.
type Variables struct {
	Local       map[string]any `json:"local"`
	Data        map[string]any `json:"data"`
	Environment map[string]any `json:"environment"`
	Collection  map[string]any `json:"collection"`
	Globals     map[string]any `json:"globals"`
}

// Info is pm.info.
type Info struct {
	EventName      string `json:"eventName"`
	Iteration      int    `json:"iteration"`
	IterationCount int    `json:"iterationCount"`
	RequestName    string `json:"requestName"`
	RequestID      string `json:"requestId"`
}

// Input is one script run.
type Input struct {
	// Event is "prerequest" or "test".
	Event     string
	Source    string
	Request   Request
	Response  *Response // nil before the request is sent
	Variables Variables
	Info      Info
}

// TestResult is one pm.test, or one key of the old tests object.
type TestResult struct {
	Name    string       `json:"name"`
	Passed  bool         `json:"passed"`
	Skipped bool         `json:"skipped"`
	Error   *ScriptError `json:"error,omitempty"`
}

// Log is one console call.
type Log struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Change records that a script set, unset or cleared a variable.
type Change struct {
	Scope string `json:"scope"`
	Op    string `json:"op"`
	Key   string `json:"key,omitempty"`
}

// ScriptError is an error the script raised, or a syntax error in it.
type ScriptError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (e *ScriptError) Error() string { return e.Name + ": " + e.Message }

// Result is what a script did.
type Result struct {
	Tests     []TestResult `json:"tests"`
	Logs      []Log        `json:"logs"`
	Changes   []Change     `json:"changes"`
	Variables Variables    `json:"variables"`
	// Request is pm.request after the script, which a pre-request script
	// may have changed.
	Request *Request `json:"request,omitempty"`
	// NextRequest is what setNextRequest named: nil when it wasn't
	// called, and "" for null, which stops the run.
	NextRequest *string `json:"nextRequest,omitempty"`
	SkipRequest bool    `json:"skipRequest"`
	// Error is an uncaught error, which ends the script. Tests that ran
	// before it keep their results.
	Error *ScriptError `json:"error,omitempty"`
}

// ErrTimeout means the script ran longer than its limit.
var ErrTimeout = errors.New("scripts: the script ran out of time")

// Sender sends a request for pm.sendRequest.
type Sender func(ctx context.Context, req Request) (*Response, error)

// Options limit and connect a session.
type Options struct {
	// Timeout bounds each script, including the requests and timers it
	// waits for. Default 30 seconds.
	Timeout time.Duration
	// Send handles pm.sendRequest. Without it, pm.sendRequest fails.
	Send Sender
	// MaxMemory stops a script whose run grows the Go heap by more than
	// this many bytes. Sobek can't count a runtime's memory, so this
	// watches the whole process: it is exact only when one script runs at
	// a time, as in a runner process. Default 256 MB; -1 turns it off.
	MaxMemory int64
	// Metrics counts the scripts sessions run; nil counts nothing.
	Metrics *Metrics
}

// Metrics counts scripts and how long they take.
type Metrics struct {
	runs     *metrics.Counter
	duration *metrics.Histogram
}

// NewMetrics registers the script metrics. A nil registry counts nothing.
func NewMetrics(r *metrics.Registry) *Metrics {
	return &Metrics{
		runs: r.NewCounter("signalbox_scripts_total",
			`Scripts run, by event ("prerequest" or "test") and outcome: "ok", "error" for a script that threw, "timeout", "memory", or "failed" when it couldn't run.`,
			"event", "outcome"),
		duration: r.NewHistogram("signalbox_script_duration_seconds",
			"How long scripts took, including the requests and timers they waited for, by event.",
			metrics.DurationBuckets, "event"),
	}
}

func (m *Metrics) observe(event string, res *Result, err error, d time.Duration) {
	if m == nil {
		return
	}
	outcome := "ok"
	switch {
	case errors.Is(err, ErrTimeout):
		outcome = "timeout"
	case errors.Is(err, ErrMemory):
		outcome = "memory"
	case err != nil:
		outcome = "failed"
	case res.Error != nil:
		outcome = "error"
	}
	// Events come from code, but only these two are counted by name.
	if event != "prerequest" && event != "test" {
		event = "other"
	}
	m.runs.Inc(event, outcome)
	m.duration.Observe(d.Seconds(), event)
}

// ErrMemory means the script used more memory than its limit.
var ErrMemory = errors.New("scripts: the script used too much memory")

// heapBytes reads the live heap cheaply, without stopping the world.
func heapBytes() int64 {
	sample := []rtmetrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	rtmetrics.Read(sample)
	return int64(min(sample[0].Value.Uint64(), math.MaxInt64)) //nolint:gosec // G115: clamped above.
}

// watchMemory calls interrupt when the heap grows by more than limit,
// until stop is called.
func watchMemory(interrupt func(error), limit int64) (stop func()) {
	if limit < 0 {
		return func() {}
	}
	start := heapBytes()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if heapBytes()-start > limit {
					interrupt(ErrMemory)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// Session runs scripts one after another in one runtime, as Postman runs a
// collection: scripts in a run can share functions they store, for
// example through eval of a global variable. A session must not be shared
// between runs, and not used from two goroutines at once.
type Session struct {
	rt   *sobek.Runtime
	opts Options
	exec sobek.Callable
}

// NewSession returns a session with a fresh runtime. The libraries load
// when a script first uses them; Pool hides that cost.
func NewSession(opts Options) (*Session, error) {
	prg, err := compileSandbox()
	if err != nil {
		return nil, fmt.Errorf("scripts: compile the sandbox: %w", err)
	}
	rt := sobek.New()
	rt.SetFieldNameMapper(sobek.TagFieldNameMapper("json", true))
	rt.SetMaxCallStackSize(2048)
	crypto := rt.NewObject()
	if err := crypto.Set("getRandomValues", func(call sobek.FunctionCall) sobek.Value {
		arr := call.Argument(0)
		if buf, ok := arr.Export().([]byte); ok {
			_, _ = rand.Read(buf)
		}
		return arr
	}); err != nil {
		return nil, err
	}
	if err := rt.Set("crypto", crypto); err != nil {
		return nil, err
	}
	if _, err := rt.RunProgram(prg); err != nil {
		return nil, fmt.Errorf("scripts: start the sandbox: %w", err)
	}
	exec, ok := sobek.AssertFunction(rt.Get("__execute"))
	if !ok {
		return nil, errors.New("scripts: the sandbox has no __execute")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxMemory == 0 {
		opts.MaxMemory = 256 << 20
	}
	return &Session{rt: rt, opts: opts, exec: exec}, nil
}

// warm loads the libraries nearly every test script uses.
func (s *Session) warm() error {
	_, err := s.rt.RunString(`__libs.chai; __libs["chai-postman"]; __libs["postman-collection"]; __libs.lodash`)
	return err
}

// Run runs one script. An error in the script is reported in the result;
// Run returns an error only if the script ran out of time or couldn't run
// at all.
func (s *Session) Run(ctx context.Context, in Input) (*Result, error) {
	start := time.Now()
	res, err := s.run(ctx, in)
	s.opts.Metrics.observe(in.Event, res, err, time.Since(start))
	return res, err
}

func (s *Session) run(ctx context.Context, in Input) (*Result, error) {
	deadline := time.Now().Add(s.opts.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// The deadline and the memory watchdog interrupt the runtime from other
	// goroutines. Once this run ends they may no longer, or a late interrupt
	// would fail the session's next script.
	var mu sync.Mutex
	running := true
	interrupt := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if running {
			s.rt.Interrupt(err)
		}
	}
	stop := context.AfterFunc(ctx, func() { interrupt(ErrTimeout) })
	stopWatch := watchMemory(interrupt, s.opts.MaxMemory)
	defer func() {
		mu.Lock()
		running = false
		mu.Unlock()
		stop()
		stopWatch()
		s.rt.ClearInterrupt()
	}()

	// The wrapper keeps the script's own line numbers.
	fn, err := s.rt.RunScript(in.Info.RequestName+" ("+in.Event+")",
		"(function (__g) { with (__g) { "+in.Source+"\n} })")
	if err != nil {
		if err := interrupted(ctx, err); err != nil {
			return nil, err
		}
		return &Result{Error: scriptError(err), Variables: in.Variables}, nil
	}
	if _, ok := sobek.AssertFunction(fn); !ok {
		return nil, errors.New("scripts: the script didn't compile to a function")
	}

	input, err := s.toJS(map[string]any{
		"request":   in.Request,
		"response":  in.Response,
		"variables": withEmptyScopes(in.Variables),
		"info":      in.Info,
	})
	if err != nil {
		return nil, s.jsError(ctx, err)
	}
	state, err := s.exec(sobek.Undefined(), input, fn)
	if err != nil {
		return nil, s.jsError(ctx, err)
	}
	obj := state.ToObject(s.rt)
	if err := s.loop(ctx, obj.Get("pending").ToObject(s.rt)); err != nil {
		return nil, err
	}
	finish, _ := sobek.AssertFunction(obj.Get("finish"))
	out, err := finish(obj)
	if err != nil {
		return nil, s.jsError(ctx, err)
	}
	var res Result
	if err := s.fromJS(out, &res); err != nil {
		return nil, s.jsError(ctx, err)
	}
	return &res, nil
}

// loop runs queued requests and timers until none are left: the event
// loop. Timers wait for real, within the script's deadline.
func (s *Session) loop(ctx context.Context, pending *sobek.Object) error {
	start := time.Now()
	for {
		n := int(pending.Get("length").ToInteger())
		if n == 0 {
			return nil
		}
		// Take the queue; callbacks may add to it.
		ops := make([]*sobek.Object, n)
		for i := range ops {
			ops[i] = pending.Get(fmt.Sprint(i)).ToObject(s.rt)
		}
		if err := pending.Set("length", 0); err != nil {
			return s.jsError(ctx, err)
		}
		sort.SliceStable(ops, func(i, j int) bool { return delay(ops[i]) < delay(ops[j]) })
		for _, op := range ops {
			done, _ := sobek.AssertFunction(op.Get("done"))
			switch op.Get("kind").String() {
			case "timer":
				wait := time.Until(start.Add(time.Duration(delay(op)) * time.Millisecond))
				if wait > 0 {
					select {
					case <-time.After(wait):
					case <-ctx.Done():
						return ErrTimeout
					}
				}
				if _, err := done(sobek.Undefined()); err != nil {
					return s.jsError(ctx, err)
				}
			case "send":
				var req Request
				if err := s.fromJS(op.Get("request"), &req); err != nil {
					return s.jsError(ctx, err)
				}
				var resp *Response
				var sendErr error
				if s.opts.Send == nil {
					sendErr = errors.New("pm.sendRequest isn't available here")
				} else {
					resp, sendErr = s.opts.Send(ctx, req)
				}
				if ctx.Err() != nil {
					return ErrTimeout
				}
				errVal, respVal := sobek.Null(), sobek.Undefined()
				if sendErr != nil {
					errVal = s.rt.ToValue(sendErr.Error())
				} else if v, err := s.toJS(resp); err == nil {
					respVal = v
				} else {
					return s.jsError(ctx, err)
				}
				if _, err := done(sobek.Undefined(), errVal, respVal); err != nil {
					return s.jsError(ctx, err)
				}
			}
		}
	}
}

// jsError returns ErrTimeout or ErrMemory if a limit stopped the
// JavaScript call that failed with err, and err otherwise.
func (s *Session) jsError(ctx context.Context, err error) error {
	if ierr := interrupted(ctx, err); ierr != nil {
		return ierr
	}
	if strings.HasPrefix(err.Error(), "scripts:") {
		return err
	}
	return fmt.Errorf("scripts: %w", err)
}

// interrupted returns ErrMemory or ErrTimeout if err comes from a limit.
func interrupted(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrMemory):
		return ErrMemory
	case errors.Is(err, ErrTimeout), ctx.Err() != nil:
		return ErrTimeout
	}
	return nil
}

func delay(op *sobek.Object) int64 {
	if v := op.Get("delay"); v != nil {
		return v.ToInteger()
	}
	return 0
}

// toJS passes a Go value into the runtime as plain JavaScript data.
func (s *Session) toJS(v any) (sobek.Value, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	parse, _ := sobek.AssertFunction(s.rt.Get("JSON").ToObject(s.rt).Get("parse"))
	return parse(sobek.Undefined(), s.rt.ToValue(string(b)))
}

// fromJS reads plain JavaScript data back into a Go value.
func (s *Session) fromJS(v sobek.Value, dst any) error {
	stringify, _ := sobek.AssertFunction(s.rt.Get("JSON").ToObject(s.rt).Get("stringify"))
	str, err := stringify(sobek.Undefined(), v)
	if err != nil {
		return fmt.Errorf("scripts: read the result: %w", err)
	}
	return json.Unmarshal([]byte(str.String()), dst)
}

func withEmptyScopes(v Variables) Variables {
	for _, m := range []*map[string]any{&v.Local, &v.Data, &v.Environment, &v.Collection, &v.Globals} {
		if *m == nil {
			*m = map[string]any{}
		}
	}
	return v
}

func scriptError(err error) *ScriptError {
	var ex *sobek.Exception
	if errors.As(err, &ex) {
		if obj, ok := ex.Value().(*sobek.Object); ok {
			name, msg := obj.Get("name"), obj.Get("message")
			if name != nil && msg != nil {
				return &ScriptError{Name: name.String(), Message: msg.String()}
			}
		}
		return &ScriptError{Name: "Error", Message: ex.Value().String()}
	}
	var syn *sobek.CompilerSyntaxError
	if errors.As(err, &syn) {
		return &ScriptError{Name: "SyntaxError", Message: strings.TrimSpace(syn.Error())}
	}
	return &ScriptError{Name: "Error", Message: err.Error()}
}

// Pool keeps a few sessions started and warmed, so a run doesn't wait for
// the libraries to load. Every session it hands out is new.
type Pool struct {
	opts  Options
	ready chan *Session
	stop  chan struct{}
	once  sync.Once
}

// NewPool starts a pool that keeps size sessions ready.
func NewPool(size int, opts Options) *Pool {
	p := &Pool{opts: opts, ready: make(chan *Session, size), stop: make(chan struct{})}
	go func() {
		for {
			s, err := NewSession(opts)
			if err == nil {
				err = s.warm()
			}
			if err != nil {
				// Session returns the error to callers instead.
				select {
				case <-p.stop:
					return
				case <-time.After(time.Second):
				}
				continue
			}
			select {
			case p.ready <- s:
			case <-p.stop:
				return
			}
		}
	}()
	return p
}

// Session returns a warmed session, or a new one if none is ready.
func (p *Pool) Session() (*Session, error) {
	select {
	case s := <-p.ready:
		return s, nil
	default:
		return NewSession(p.opts)
	}
}

// Close stops refilling the pool.
func (p *Pool) Close() { p.once.Do(func() { close(p.stop) }) }
