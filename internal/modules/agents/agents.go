// SPDX-License-Identifier: Apache-2.0

// Package agents is the module for agents: `signalbox agent` processes on
// people's machines that send their owner's requests from there, so
// localhost and private networks work. An agent asks for work over HTTPS,
// holding a request open until there is some, so it opens no port. See
// docs/adr/0037-local-agent.md.
package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Errors the module returns.
var (
	// ErrNotFound means the agent or job doesn't exist, or isn't the
	// user's.
	ErrNotFound = errors.New("agents: not found")
)

// Timing of the conversation with agents.
const (
	// MaxWait is the longest an agent's request for work is held.
	MaxWait = 25 * time.Second
	// online is how recently an agent must have asked for work to count
	// as connected.
	online = 45 * time.Second
	// pickUp is how long a job waits for its agent to take it.
	pickUp = 15 * time.Second
	// grace is how long past the request's own timeout a result may take.
	grace = 15 * time.Second
	// queued bounds the jobs waiting for one agent.
	queued = 64
)

// Agent is a `signalbox agent` process of a user.
type Agent struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Hostname   string
	OS         string
	Version    string
	CreatedAt  time.Time
	LastSeenAt time.Time
	// Online says it asked for work recently.
	Online bool
}

// Input is what an agent says about itself when it connects.
type Input struct {
	Name     string
	Hostname string
	OS       string
	Version  string
}

// Job is a request an agent sends.
type Job struct {
	ID      uuid.UUID
	Request runner.Request
	// result takes the one result; it has room for it and is never
	// closed, so a late result can't block or panic.
	result chan runner.Result
	// gone is closed when the sender stops waiting.
	gone     chan struct{}
	goneOnce sync.Once
}

func (j *Job) giveUp() { j.goneOnce.Do(func() { close(j.gone) }) }

// Module keeps agents and hands them their owners' requests. The jobs live
// in this process: an agent and the sends for it must reach the same
// process, as with --roles=all.
type Module struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	now    func() time.Time
	// pickUp is how long a job waits for its agent to take it.
	pickUp time.Duration

	mu sync.Mutex
	// queues hold each agent's jobs waiting to be taken.
	queues map[uuid.UUID]chan *Job
	// polled is when each agent last asked for work.
	polled map[uuid.UUID]time.Time
	// taken are jobs an agent took, by job, waiting for their results.
	taken map[uuid.UUID]takenJob
}

type takenJob struct {
	agent uuid.UUID
	job   *Job
}

// New returns the module.
func New(db *pgxpool.Pool, logger *slog.Logger) *Module {
	return &Module{db: db, logger: logger, now: time.Now, pickUp: pickUp,
		queues: map[uuid.UUID]chan *Job{}, polled: map[uuid.UUID]time.Time{}, taken: map[uuid.UUID]takenJob{}}
}

const columns = `id, user_id, name, hostname, os, version, created_at, last_seen_at`

func readAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Hostname, &a.OS, &a.Version, &a.CreatedAt, &a.LastSeenAt)
	return a, err
}

func (m *Module) online(a *Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.Online = m.now().Sub(m.polled[a.ID]) < online
}

// Connect registers an agent of the user, or finds the one with its name:
// an agent that restarts is the same agent.
func (m *Module) Connect(ctx context.Context, userID uuid.UUID, in Input) (Agent, error) {
	var errs validate.Errors
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		errs.Add("/name", "length", "Name the agent in 1 to 100 characters, such as your machine's name.")
	}
	if len(in.Hostname) > 255 || len(in.OS) > 64 || len(in.Version) > 64 {
		errs.Add("/hostname", "length", "Keep the hostname to 255 characters, and the system and version to 64.")
	}
	if len(errs) > 0 {
		return Agent{}, errs
	}
	a, err := readAgent(m.db.QueryRow(ctx, `
		INSERT INTO agents (id, user_id, name, hostname, os, version) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, name) DO UPDATE SET hostname = excluded.hostname, os = excluded.os, version = excluded.version, last_seen_at = now()
		RETURNING `+columns, id.New(), userID, in.Name, in.Hostname, in.OS, in.Version))
	if err != nil {
		return Agent{}, fmt.Errorf("connect an agent: %w", err)
	}
	m.logger.InfoContext(ctx, "agent connected", id.LogAttr("agent_id", id.Agent, a.ID), id.LogAttr("user_id", id.User, userID))
	return a, nil
}

// List lists the user's agents by name. People see only their own.
func (m *Module) List(ctx context.Context, userID uuid.UUID) ([]Agent, error) {
	rows, err := m.db.Query(ctx, `SELECT `+columns+` FROM agents WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Agent, error) { return readAgent(row) })
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	for i := range list {
		m.online(&list[i])
	}
	return list, nil
}

// Get returns one of the user's agents.
func (m *Module) Get(ctx context.Context, userID, agentID uuid.UUID) (Agent, error) {
	a, err := readAgent(m.db.QueryRow(ctx, `SELECT `+columns+` FROM agents WHERE id = $1 AND user_id = $2`, agentID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("get agent: %w", err)
	}
	m.online(&a)
	return a, nil
}

// Delete forgets one of the user's agents. If it's still running, its
// next request for work fails, and it says so.
func (m *Module) Delete(ctx context.Context, userID, agentID uuid.UUID) error {
	tag, err := m.db.Exec(ctx, `DELETE FROM agents WHERE id = $1 AND user_id = $2`, agentID, userID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	m.mu.Lock()
	delete(m.polled, agentID)
	m.mu.Unlock()
	return nil
}

func (m *Module) queue(agentID uuid.UUID) chan *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.queues[agentID]
	if !ok {
		q = make(chan *Job, queued)
		m.queues[agentID] = q
	}
	return q
}

// Next waits up to wait for a job for one of the user's agents, and
// returns nil when there is none. Asking keeps the agent online.
func (m *Module) Next(ctx context.Context, userID, agentID uuid.UUID, wait time.Duration) (*Job, error) {
	tag, err := m.db.Exec(ctx, `UPDATE agents SET last_seen_at = now() WHERE id = $1 AND user_id = $2`, agentID, userID)
	if err != nil {
		return nil, fmt.Errorf("find agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	wait = min(max(wait, 0), MaxWait)
	q := m.queue(agentID)
	m.mu.Lock()
	m.polled[agentID] = m.now()
	m.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case j := <-q:
			m.mu.Lock()
			// A job whose sender gave up already isn't worth sending.
			select {
			case <-j.gone:
				m.mu.Unlock()
				continue
			default:
			}
			m.taken[j.ID] = takenJob{agent: agentID, job: j}
			m.polled[agentID] = m.now()
			m.mu.Unlock()
			return j, nil
		case <-timer.C:
			m.mu.Lock()
			m.polled[agentID] = m.now()
			m.mu.Unlock()
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Complete hands back the result of a job one of the user's agents took.
func (m *Module) Complete(ctx context.Context, userID, agentID, jobID uuid.UUID, res runner.Result) error {
	if _, err := m.Get(ctx, userID, agentID); err != nil {
		return err
	}
	m.mu.Lock()
	t, ok := m.taken[jobID]
	if ok && t.agent == agentID {
		delete(m.taken, jobID)
	}
	m.mu.Unlock()
	if !ok || t.agent != agentID {
		return ErrNotFound
	}
	select {
	case t.job.result <- res:
	default:
		// It has a result already.
	}
	return nil
}

// Runner sends a request; the client module's engine takes one.
type Runner interface {
	Run(ctx context.Context, req runner.Request) runner.Result
	// Name is the agent's name.
	Name() string
}

// Runner returns a runner that sends through one of the user's agents.
// Only the agent's owner gets one: the server never hands anyone's
// request to someone else's agent.
func (m *Module) Runner(ctx context.Context, userID, agentID uuid.UUID) (Runner, error) {
	a, err := m.Get(ctx, userID, agentID)
	if err != nil {
		return nil, err
	}
	return &AgentRunner{m: m, agent: a}, nil
}

// AgentRunner sends requests through an agent. It is a client.Runner.
type AgentRunner struct {
	m     *Module
	agent Agent
}

// Run hands the request to the agent and waits for its result. An agent
// that doesn't take it in time is reported as offline.
func (r *AgentRunner) Run(ctx context.Context, req runner.Request) runner.Result {
	j := &Job{ID: id.New(), Request: req, result: make(chan runner.Result, 1), gone: make(chan struct{})}
	defer j.giveUp()
	m := r.m
	select {
	case m.queue(r.agent.ID) <- j:
	default:
		return failure("agent-busy", fmt.Sprintf("The agent %s has too many requests waiting. Try again in a moment.", r.agent.Name))
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	picked := time.NewTimer(m.pickUp)
	defer picked.Stop()
	done := time.NewTimer(m.pickUp + timeout + grace)
	defer done.Stop()
	for {
		select {
		case res := <-j.result:
			return res
		case <-picked.C:
			// Checked and given up together, so an agent can't take it
			// in between.
			m.mu.Lock()
			_, taken := m.taken[j.ID]
			if !taken {
				j.giveUp()
			}
			m.mu.Unlock()
			if !taken {
				return failure("agent-offline", fmt.Sprintf("The agent %s didn't take the request. Check that `signalbox agent` is running on %s.", r.agent.Name, orName(r.agent.Hostname, "its machine")))
			}
		case <-done.C:
			r.forget(j)
			return failure("agent-timeout", fmt.Sprintf("The agent %s took the request but didn't answer in time.", r.agent.Name))
		case <-ctx.Done():
			r.forget(j)
			return failure("timeout", "The request was cancelled before the agent answered.")
		}
	}
}

// Name is the agent's name.
func (r *AgentRunner) Name() string { return r.agent.Name }

func (r *AgentRunner) forget(j *Job) {
	r.m.mu.Lock()
	delete(r.m.taken, j.ID)
	r.m.mu.Unlock()
}

func failure(code, msg string) runner.Result {
	return runner.Result{Failure: &runner.Failure{Code: code, Message: msg}}
}

func orName(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
