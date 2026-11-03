// SPDX-License-Identifier: Apache-2.0

// Package canvas answers what a project's canvas shows live: each service's
// traffic and the lines between them, from the spans of one environment.
// See docs/adr/0045-live-canvas-api.md.
package canvas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

const (
	// recent is the time rates and latency cover.
	recent = 5 * time.Minute
	// seenFor is how long a service or line stays after its last traffic,
	// at a rate of zero, so a quiet night doesn't empty the canvas.
	seenFor = time.Hour
	// graphDelay is how far behind the service graph is: a minute is built
	// 90 seconds after it ends, and ticks come every 15 seconds.
	graphDelay = 105 * time.Second
	// cacheFor is how long an answer serves everyone watching.
	cacheFor = 5 * time.Second
	// keepAnswers is how many past answers changes can be told from: a
	// minute's worth, longer than a browser usually misses.
	keepAnswers = 12
	// forgetAfter drops an environment nobody has watched for this long.
	forgetAfter = 2 * time.Minute
	// readTimeout bounds one build of an answer.
	readTimeout = 10 * time.Second
	// maxServicePages bounds the services read, at 200 a page.
	maxServicePages = 50
)

// ErrNotFound means the project or environment doesn't exist, the
// environment isn't the project's, or the user isn't a member.
var ErrNotFound = errors.New("not found")

// Service is one service on the canvas, or one seen in spans that isn't.
type Service struct {
	// ID is the node's ID: the service's, on the canvas, and otherwise
	// "seen:" and its name.
	ID string
	// Name is the service.name of its spans.
	Name     string
	OnCanvas bool
	// Rate is requests handled a second, ErrorRate the share that failed.
	Rate, ErrorRate float64
	// P95Ms is 0 without requests.
	P95Ms float64
}

// Line is who calls whom, between node IDs. A database's or host's node ID
// is its kind, a colon and its address.
type Line struct {
	ID       string
	From, To string
	FromName string
	ToName   string
	// ToKind is service, database or external.
	ToKind string
	// System is a database's db.system.
	System string
	// Rate is calls a second, ErrorRate the share that failed.
	Rate, ErrorRate float64
}

// Answer is what changed since the answer the caller has, or everything.
type Answer struct {
	// Tag names the whole state. Sent back, it asks for what changed.
	Tag string
	// NotModified means the caller's answer is still current.
	NotModified bool
	// Since is the tag this answer's changes apply to; "" means it holds
	// everything.
	Since    string
	Services []Service
	Lines    []Line
	// RemovedServices and RemovedLines are IDs gone since Since.
	RemovedServices, RemovedLines []string
}

// Access is what the canvas needs to know about environments.
type Access interface {
	GetEnvironment(ctx context.Context, userID, environmentID uuid.UUID) (workspaces.Environment, workspaces.Role, error)
}

// Services lists a project's services.
type Services interface {
	ListServices(ctx context.Context, userID, projectID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[apis.Service], error)
}

// Module answers for the canvas. Answers are cached per environment, so
// fifty people watching one project cost one read of ClickHouse every five
// seconds; access is checked on every request.
type Module struct {
	conn     clickhouse.Conn
	access   Access
	services Services
	now      func() time.Time

	mu   sync.Mutex
	envs map[uuid.UUID]*environment
}

// environment is one environment's cached answers. Its lock is held while
// an answer is built, so only one request reads while the others wait.
type environment struct {
	mu   sync.Mutex
	at   time.Time // when the newest answer or failure was made
	err  error     // the last read's failure, repeated until at expires
	past []state   // oldest first
	used time.Time // guarded by Module.mu
}

type state struct {
	tag      string
	services []Service
	lines    []Line
}

// New returns the module.
func New(conn clickhouse.Conn, access Access, services Services) *Module {
	return &Module{conn: conn, access: access, services: services, now: time.Now, envs: map[uuid.UUID]*environment{}}
}

// Live returns what the canvas shows for an environment, as changes since
// the state tagged since when that's still known. Every member of the
// project may read it.
func (m *Module) Live(ctx context.Context, userID, projectID, environmentID uuid.UUID, since string) (Answer, error) {
	env, _, err := m.access.GetEnvironment(ctx, userID, environmentID)
	if errors.Is(err, workspaces.ErrNotFound) || err == nil && env.ProjectID != projectID {
		return Answer{}, ErrNotFound
	}
	if err != nil {
		return Answer{}, fmt.Errorf("check access: %w", err)
	}

	e := m.environment(environmentID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if now := m.now(); now.Sub(e.at) >= cacheFor {
		next, err := m.read(ctx, userID, projectID, environmentID, now)
		// Someone leaving says nothing about ClickHouse; the next in line
		// reads instead.
		if err != nil && !errors.Is(err, context.Canceled) {
			e.err, e.at = err, now
		}
		if err != nil {
			return Answer{}, err
		}
		e.err, e.at = nil, now
		if len(e.past) == 0 || e.past[len(e.past)-1].tag != next.tag {
			e.past = append(e.past, next)
			if len(e.past) > keepAnswers {
				e.past = e.past[1:]
			}
		}
	}
	if e.err != nil {
		return Answer{}, e.err
	}
	latest := e.past[len(e.past)-1]
	if since == latest.tag {
		return Answer{Tag: latest.tag, NotModified: true}, nil
	}
	for _, old := range e.past {
		if old.tag == since {
			return changes(old, latest), nil
		}
	}
	return Answer{Tag: latest.tag, Services: latest.services, Lines: latest.lines}, nil
}

// environment returns the environment's cache entry, and forgets those
// nobody has asked about lately. Forgetting one in use costs a read at most.
func (m *Module) environment(environmentID uuid.UUID) *environment {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.envs {
		if now.Sub(e.used) > forgetAfter {
			delete(m.envs, k)
		}
	}
	e := m.envs[environmentID]
	if e == nil {
		e = &environment{}
		m.envs[environmentID] = e
	}
	e.used = now
	return e
}

func (m *Module) read(ctx context.Context, userID, projectID, environmentID uuid.UUID, now time.Time) (state, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	services, err := m.allServices(ctx, userID, projectID)
	if err != nil {
		return state{}, err
	}
	seen := now.Add(-seenFor)
	// The rollups hold whole minutes, the newest still filling. Dividing by
	// the time since the oldest one's start keeps the rate right throughout.
	from := now.Add(-recent).Truncate(time.Minute)
	traffic, err := telemetry.RecentServices(ctx, m.conn, environmentID, seen, from)
	if err != nil {
		return state{}, err
	}
	// Lines are complete only for minutes built, so they cover the five
	// before those being built.
	to := now.Add(-graphDelay).Truncate(time.Minute)
	edges, err := telemetry.RecentEdges(ctx, m.conn, environmentID, seen, to.Add(-recent), to)
	if err != nil {
		return state{}, err
	}
	return assemble(services, traffic, now.Sub(from), edges, recent), nil
}

func (m *Module) allServices(ctx context.Context, userID, projectID uuid.UUID) ([]apis.Service, error) {
	var out []apis.Service
	var after uuid.UUID
	for range maxServicePages {
		page, err := m.services.ListServices(ctx, userID, projectID, workspaces.PageRequest{After: after, Limit: 200})
		if errors.Is(err, apis.ErrNotFound) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if !page.More || len(page.Items) == 0 {
			break
		}
		after = page.Items[len(page.Items)-1].ID
	}
	return out, nil
}

// spanName is the service.name a canvas service's spans carry: the one it
// was told, or its slug.
func spanName(s apis.Service) string {
	if s.TelemetryServiceName != "" {
		return s.TelemetryServiceName
	}
	return s.Slug
}

// assemble matches what traffic shows with the canvas's services. A span's
// service.name matches at read time, so setting a service's telemetry name
// takes effect at once, spans already stored included. If two services
// claim one name, the oldest gets it, and a told name beats a slug.
func assemble(services []apis.Service, traffic []telemetry.RecentService, serviceWindow time.Duration, edges []telemetry.RecentEdge, lineWindow time.Duration) state {
	nodes := make(map[string]string, len(services)) // span name to node ID
	for _, told := range []bool{true, false} {
		for _, s := range services {
			name := spanName(s)
			if _, taken := nodes[name]; !taken && (s.TelemetryServiceName != "") == told {
				nodes[name] = id.Format(id.Service, s.ID)
			}
		}
	}
	byName := make(map[string]telemetry.RecentService, len(traffic))
	for _, t := range traffic {
		byName[t.Service] = t
	}
	out := state{services: make([]Service, 0, len(services)+len(traffic))}
	for _, s := range services {
		nodeID := id.Format(id.Service, s.ID)
		node := Service{ID: nodeID, Name: spanName(s), OnCanvas: true}
		if nodes[node.Name] == nodeID {
			fill(&node, byName[node.Name], serviceWindow)
		}
		out.services = append(out.services, node)
	}
	for _, t := range traffic {
		if _, onCanvas := nodes[t.Service]; !onCanvas {
			node := Service{ID: "seen:" + t.Service, Name: t.Service}
			fill(&node, t, serviceWindow)
			out.services = append(out.services, node)
		}
	}
	sort.SliceStable(out.services, func(i, j int) bool { return out.services[i].ID < out.services[j].ID })
	out.lines = lines(nodes, edges, lineWindow)
	out.tag = tagOf(out)
	return out
}

func fill(node *Service, t telemetry.RecentService, window time.Duration) {
	if t.Calls == 0 {
		return
	}
	node.Rate = rounded(float64(t.Calls) / window.Seconds())
	node.ErrorRate = errorRate(t.Calls, t.Errors)
	node.P95Ms = rounded(float64(t.P95) / float64(time.Millisecond))
}

// lines resolves the graph's calls to nodes. A call to an address that a
// canvas service answers to is a call to that service, even before it sends
// spans. Calls that resolve to one pair are one line.
func lines(nodes map[string]string, edges []telemetry.RecentEdge, window time.Duration) []Line {
	type total struct {
		line          Line
		calls, errors uint64
	}
	totals := map[string]*total{}
	for _, e := range edges {
		from, ok := nodes[e.From]
		if !ok {
			from = "seen:" + e.From
		}
		to, kind := nodes[e.Target], string(telemetry.TargetService)
		if to == "" {
			kind = string(e.TargetKind)
			if e.TargetKind == telemetry.TargetService {
				to = "seen:" + e.Target
			} else {
				to = kind + ":" + e.Target
			}
		}
		if from == to {
			continue
		}
		lineID := from + ">" + to
		t := totals[lineID]
		if t == nil {
			t = &total{line: Line{ID: lineID, From: from, To: to, FromName: e.From, ToName: e.Target, ToKind: kind}}
			totals[lineID] = t
		}
		if t.line.System == "" && kind == string(telemetry.TargetDatabase) {
			t.line.System = e.System
		}
		t.calls += e.Calls
		t.errors += e.Errors
	}
	out := make([]Line, 0, len(totals))
	for _, t := range totals {
		t.line.Rate = rounded(float64(t.calls) / window.Seconds())
		t.line.ErrorRate = errorRate(t.calls, t.errors)
		out = append(out, t.line)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func errorRate(calls, errors uint64) float64 {
	if calls == 0 {
		return 0
	}
	return math.Round(float64(errors)/float64(calls)*10000) / 10000
}

// rounded keeps three significant digits. More would only be noise, and
// would make unchanged values look changed on every poll.
func rounded(v float64) float64 {
	if v == 0 {
		return 0
	}
	scale := math.Pow(10, 2-math.Floor(math.Log10(math.Abs(v))))
	return math.Round(v*scale) / scale
}

// tagOf names a state by its content, so every API process gives the same
// answer the same tag.
func tagOf(s state) string {
	h := sha256.New()
	for _, v := range s.services {
		fmt.Fprintf(h, "%q %q %v %v %v %v\n", v.ID, v.Name, v.OnCanvas, v.Rate, v.ErrorRate, v.P95Ms)
	}
	for _, l := range s.lines {
		fmt.Fprintf(h, "%q %q %q %v %v\n", l.ID, l.ToKind, l.System, l.Rate, l.ErrorRate)
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:12]) + `"`
}

// changes says how to get from old to latest.
func changes(old, latest state) Answer {
	out := Answer{Tag: latest.tag, Since: old.tag}
	out.Services, out.RemovedServices = diff(old.services, latest.services, func(s Service) string { return s.ID })
	out.Lines, out.RemovedLines = diff(old.lines, latest.lines, func(l Line) string { return l.ID })
	return out
}

func diff[T comparable](old, latest []T, key func(T) string) (changed []T, removed []string) {
	before := make(map[string]T, len(old))
	for _, v := range old {
		before[key(v)] = v
	}
	for _, v := range latest {
		k := key(v)
		if was, ok := before[k]; !ok || was != v {
			changed = append(changed, v)
		}
		delete(before, k)
	}
	for k := range before {
		removed = append(removed, k)
	}
	sort.Strings(removed)
	return changed, removed
}
