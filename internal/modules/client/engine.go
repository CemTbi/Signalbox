// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/scripts"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

// Engine runs plans: requests in order, with their scripts, variables and
// auth, through the runner. It doesn't touch the database, so sending one
// request, a run on the server and an offline run of the CLI behave the
// same. See docs/adr/0033-collection-runs.md.
type Engine struct {
	runner  Runner
	scripts ScriptPool
	// slots bounds how many sets of scripts run at once.
	slots chan struct{}
}

// NewEngine returns an engine that sends through run and runs scripts from
// pool, which may be nil when scripts can't run. Give the pool the
// engine's ScriptSender.
func NewEngine(run Runner, pool ScriptPool) *Engine {
	return &Engine{runner: run, scripts: pool, slots: make(chan struct{}, scriptSlots)}
}

// Engine returns the engine the module sends with.
func (m *Module) Engine() *Engine {
	return &Engine{runner: m.runner, scripts: m.scripts, slots: m.scriptSlots}
}

// CanSend reports whether the engine has a runner to send with.
func (e *Engine) CanSend() bool { return e.runner != nil }

// Link is the scripts one level of a plan contributes to a step: the
// collection's, a folder's or the request's.
type Link struct {
	// From names it.
	From    string
	Scripts Scripts
}

// Step is one request of a plan.
type Step struct {
	ItemID uuid.UUID
	Name   string
	// Path is the folders the request is in, joined by " / ".
	Path    string
	Request runner.Request
	// Auth is applied after the variables; nil for none.
	Auth *Auth
	// Chain is the scripts that run around the request, outermost first.
	Chain []Link
}

func (s Step) chain() chain {
	c := chain{name: s.Name, itemID: s.ItemID}
	if c.name == "" {
		c.name = "Request"
	}
	for _, l := range s.Chain {
		c.add(l.From, l.Scripts)
	}
	return c
}

// Plan is what a run sends: requests in order, and the collection's
// variables.
type Plan struct {
	Name      string
	Variables map[string]string
	Steps     []Step
}

// Run limits. See ADR-0033.
const (
	MaxIterations = 1000
	MaxDataRows   = 1000
	MaxRunSteps   = 10000
	MaxRunDelay   = time.Minute
)

// RunOptions are how a plan runs.
type RunOptions struct {
	// Iterations is how many times the plan runs; 0 runs it once per data
	// row, or once.
	Iterations int
	// Data are the iterations' rows. Iterations past the last reuse it.
	Data []map[string]any
	// Environment is the environment's variables; Secrets names those
	// that are secret.
	Environment map[string]string
	Secrets     []string
	// Delay is the pause between requests.
	Delay time.Duration
	// Bail stops the run at the first step that fails.
	Bail bool
}

// StepResult is a step as it ran, with secrets masked.
type StepResult struct {
	// Iteration counts from 0.
	Iteration int
	// Index is the step's place in the plan.
	Index int
	// Entry is what was sent and what came back, as history would keep it
	// but whole. Its IDs and time are unset.
	Entry Entry
	// Note says why the run went on differently, such as a
	// setNextRequest that named no request.
	Note string
}

// Failed reports whether the step counts against a run: it got no
// response, other than by being skipped, a script failed or a test failed.
func (r StepResult) Failed() bool {
	e := r.Entry
	if f := e.Result.Failure; f != nil && f.Code != "skipped" {
		return true
	}
	if s := e.Scripts; s != nil {
		if len(s.Errors) > 0 {
			return true
		}
		for _, t := range s.Tests {
			if !t.Passed && !t.Skipped {
				return true
			}
		}
	}
	return false
}

// Why a run ended before its last step.
const (
	StoppedBail  = "bail"
	StoppedSteps = "steps"
)

// ErrStopRun, returned by a run's emit function, ends the run quietly.
var ErrStopRun = errors.New("client: stop the run")

// state is what carries from one step of a run to the next.
type state struct {
	vars       scripts.Variables
	session    *scripts.Session
	iteration  int
	iterations int
	// stored are the secrets' values before any script changed them.
	stored  map[string]string
	secrets []string
}

// stepOut is a step as it ran, unmasked.
type stepOut struct {
	req         runner.Request
	auth        *Auth
	result      runner.Result
	scripts     *ScriptOutcome
	unresolved  []string
	credentials []string
	// values are the variables the request was filled in with.
	values map[string]string
	// next is what setNextRequest named, or nil.
	next *string
}

// Run runs a plan, handing each step to emit as it finishes. It returns
// why the run ended early, or "" when every step ran, and an error when it
// couldn't go on: ctx ended, scripts couldn't start, or emit failed.
func (e *Engine) Run(ctx context.Context, p Plan, opts RunOptions, emit func(StepResult) error) (string, error) {
	if e.runner == nil {
		return "", ErrRunnerUnavailable
	}
	iterations := opts.Iterations
	if iterations <= 0 {
		iterations = max(len(opts.Data), 1)
	}
	st := &state{
		vars:       scripts.Variables{Collection: anyMap(p.Variables), Environment: anyMap(opts.Environment)},
		iterations: iterations, secrets: opts.Secrets, stored: map[string]string{},
	}
	for _, k := range opts.Secrets {
		st.stored[k] = opts.Environment[k]
	}
	byName := map[string]int{}
	for i, s := range p.Steps {
		if _, ok := byName[s.Name]; !ok && s.Name != "" {
			byName[s.Name] = i
		}
		if s.ItemID != uuid.Nil {
			byName[id.Format(id.Item, s.ItemID)] = i
		}
	}
	count := 0
	for it := 0; it < iterations; it++ {
		st.iteration = it
		st.vars.Data = nil
		if n := len(opts.Data); n > 0 {
			st.vars.Data = opts.Data[min(it, n-1)]
		}
		for i := 0; i < len(p.Steps); {
			if count >= MaxRunSteps {
				return StoppedSteps, nil
			}
			if count > 0 && opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			count++
			s := p.Steps[i]
			out, err := e.step(ctx, st, s)
			if errors.Is(err, ErrResolvedTooLarge) {
				out = stepOut{req: s.Request, result: runner.Result{Failure: &runner.Failure{Code: "too-large",
					Message: "The request is too large once its variables are filled in, so it wasn't sent."}}}
			} else if err != nil {
				return "", err
			}
			r := StepResult{Iteration: it, Index: i, Entry: out.masked(st)}
			next := i + 1
			if out.next != nil {
				switch to, ok := byName[*out.next]; {
				case *out.next == "":
					next = len(p.Steps)
				case ok:
					next = to
				default:
					next = len(p.Steps)
					r.Note = fmt.Sprintf("setNextRequest named %q, which isn't in this run, so the iteration ended.", *out.next)
				}
			}
			if err := emit(r); err != nil {
				return "", err
			}
			if opts.Bail && r.Failed() {
				return StoppedBail, nil
			}
			i = next
		}
	}
	return "", nil
}

// step runs one request with its scripts.
func (e *Engine) step(ctx context.Context, st *state, s Step) (stepOut, error) {
	req, auth := s.Request, s.Auth
	values := flatten(st.vars)
	c := s.chain()
	var run *scriptRun
	send := true
	if !c.empty() {
		if st.session == nil {
			if e.scripts == nil {
				// Scripts run beside the runner, which always gets a pool.
				return stepOut{}, ErrRunnerUnavailable
			}
			session, err := e.scripts.Session()
			if err != nil {
				return stepOut{}, fmt.Errorf("start a script session: %w", err)
			}
			st.session = session
		}
		vars := st.vars
		vars.Local = nil
		run = &scriptRun{e: e, session: st.session, chain: c, vars: vars, iteration: st.iteration, iterations: st.iterations}
		var err error
		if send, err = run.pre(ctx, &req); err != nil {
			return stepOut{}, err
		}
		values = run.values()
	}
	res := newResolver(values, st.secrets, time.Now())
	req, err := res.request(req)
	if err != nil {
		return stepOut{}, err
	}
	auth = auth.resolve(res)
	if res.budget < 0 {
		return stepOut{}, ErrResolvedTooLarge
	}
	out := stepOut{auth: auth, credentials: auth.apply(&req), unresolved: res.Unresolved(), values: values}
	switch {
	case send:
		out.result = e.runner.Run(ctx, req)
		if run != nil && out.result.Response != nil {
			if err := run.test(ctx, req, out.result.Response, out.result.Timings.Total.Milliseconds()); err != nil {
				return stepOut{}, err
			}
		}
	case run.outcome.Skipped:
		out.result.Failure = &runner.Failure{Code: "skipped", Message: "A pre-request script skipped the request, so it wasn't sent."}
	default:
		last := run.outcome.Errors[len(run.outcome.Errors)-1]
		out.result.Failure = &runner.Failure{Code: "script",
			Message: fmt.Sprintf("The pre-request script of %s failed, so the request wasn't sent. %s: %s", last.From, last.Name, last.Message)}
	}
	out.req = req
	if run != nil {
		out.scripts, out.next = &run.outcome, run.next
		// Local values last for one request.
		st.vars = run.vars
		st.vars.Local = nil
	}
	return out, nil
}

// flatten gives the variables for {{name}} when no script runs, narrowest
// last, so it wins.
func flatten(v scripts.Variables) map[string]string {
	return (&scriptRun{vars: v}).values()
}

// entry is what the step sent and got, whole.
func (o stepOut) entry() Entry {
	e := Entry{
		Unresolved: o.unresolved, Auth: o.auth.kept(), Scripts: o.scripts,
		Method: strings.ToUpper(strings.TrimSpace(o.req.Method)), URL: strings.TrimSpace(o.req.URL),
		Headers: redact(o.req.Headers), Body: o.req.Body, Result: o.result,
	}
	return e
}

// mask hides the run's secrets in what the step sent and got: their stored
// values, their values now, and what auth sent.
func (o stepOut) mask(st *state) masker {
	hidden := o.credentials
	for _, k := range st.secrets {
		hidden = append(hidden, o.values[k])
	}
	return newMasker(st.stored, st.secrets, hidden...)
}

func (o stepOut) masked(st *state) Entry {
	return o.entry().masked(o.mask(st))
}

// masked returns a copy of the entry with the masker's values hidden.
func (e Entry) masked(mask masker) Entry {
	kept := e
	kept.Scripts = e.Scripts.mask(mask)
	kept.URL, kept.Headers, kept.Body = mask.string(kept.URL), mask.headers(kept.Headers), mask.bytes(kept.Body)
	if a := kept.Auth; a != nil {
		masked := *a
		masked.Username = mask.string(a.Username)
		kept.Auth = &masked
	}
	if f := e.Result.Failure; f != nil {
		// Failure messages can name the host, which a secret may fill in.
		masked := *f
		masked.Message = mask.string(f.Message)
		kept.Result.Failure = &masked
	}
	if r := e.Result.Response; r != nil {
		masked := *r
		masked.Headers, masked.URL, masked.Body = mask.headers(r.Headers), mask.string(r.URL), mask.bytes(r.Body)
		kept.Result.Response = &masked
	}
	return kept
}
