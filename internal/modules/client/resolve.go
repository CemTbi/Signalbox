// SPDX-License-Identifier: Apache-2.0

package client

import (
	"cmp"
	"errors"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
)

// ErrResolvedTooLarge means filling in variables made the request larger
// than the runner sends.
var ErrResolvedTooLarge = errors.New("client: the request is too large once its variables are filled in")

// How far filling in variables goes.
const (
	// maxNesting is how deep variables' values may name other variables.
	maxNesting = 8
	// maxResolved bounds the filled-in request, so variables that name
	// each other twice over can't grow it without end.
	maxResolved = 12 << 20
)

// resolver fills in {{name}} in a request from an environment's variables
// and the built-ins: {{$guid}}, {{$timestamp}}, {{$isoTimestamp}} and
// {{$randomInt}}. Plain values may name other variables; secrets are used
// as they are. A name with no value stays as it is, and is reported.
type resolver struct {
	values     map[string]string
	secrets    map[string]bool
	now        time.Time
	unresolved map[string]bool
	budget     int
}

func newResolver(values map[string]string, secrets []string, now time.Time) *resolver {
	r := &resolver{values: values, secrets: map[string]bool{}, now: now, unresolved: map[string]bool{}, budget: maxResolved}
	for _, s := range secrets {
		r.secrets[s] = true
	}
	return r
}

// request fills in a request's URL, headers and body.
func (r *resolver) request(req runner.Request) (runner.Request, error) {
	out := req
	out.URL = r.expand(req.URL, 0)
	out.Headers = make([]runner.Header, len(req.Headers))
	for i, h := range req.Headers {
		out.Headers[i] = runner.Header{Key: r.expand(h.Key, 0), Value: r.expand(h.Value, 0)}
	}
	if len(req.Body) > 0 {
		out.Body = []byte(r.expand(string(req.Body), 0))
	}
	if r.budget < 0 {
		return runner.Request{}, ErrResolvedTooLarge
	}
	return out, nil
}

// Unresolved lists the names that had no value, sorted.
func (r *resolver) Unresolved() []string {
	names := make([]string, 0, len(r.unresolved))
	for n := range r.unresolved {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func (r *resolver) expand(s string, depth int) string {
	if !strings.Contains(s, "{{") {
		r.budget -= len(s)
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			break
		}
		b.WriteString(s[:start])
		whole := s[start : start+2+end+2]
		b.WriteString(r.value(whole, strings.TrimSpace(s[start+2:start+2+end]), depth))
		s = s[start+2+end+2:]
		if b.Len() > r.budget {
			r.budget = -1
			return ""
		}
	}
	b.WriteString(s)
	r.budget -= b.Len()
	return b.String()
}

// value is what one {{name}} becomes.
func (r *resolver) value(whole, name string, depth int) string {
	if strings.HasPrefix(name, "$") {
		if v, ok := r.builtin(name); ok {
			return v
		}
		r.unresolved[name] = true
		return whole
	}
	v, ok := r.values[name]
	switch {
	case !ok, depth >= maxNesting:
		r.unresolved[name] = true
		return whole
	case r.secrets[name]:
		return v
	default:
		// The nested value is counted once, as part of this one.
		saved := r.budget
		v = r.expand(v, depth+1)
		if r.budget >= 0 {
			r.budget = saved
		}
		return v
	}
}

func (r *resolver) builtin(name string) (string, bool) {
	switch name {
	case "$guid", "$randomUUID":
		return uuid.NewString(), true
	case "$timestamp":
		return strconv.FormatInt(r.now.Unix(), 10), true
	case "$isoTimestamp":
		return r.now.UTC().Format("2006-01-02T15:04:05.000Z"), true
	case "$randomInt":
		return strconv.Itoa(rand.IntN(1001)), true //nolint:gosec // a test value, not a secret
	}
	return "", false
}

// masker replaces secret values with ••• in what history keeps.
type masker struct{ r *strings.Replacer }

func newMasker(values map[string]string, secrets []string) masker {
	vals := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if v := values[s]; v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return masker{}
	}
	// Longer values first, so a secret inside another doesn't leave the
	// rest of the longer one behind.
	slices.SortFunc(vals, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, redacted)
	}
	return masker{strings.NewReplacer(pairs...)}
}

func (m masker) string(s string) string {
	if m.r == nil {
		return s
	}
	return m.r.Replace(s)
}

func (m masker) bytes(b []byte) []byte {
	if m.r == nil {
		return b
	}
	return []byte(m.r.Replace(string(b)))
}

func (m masker) headers(hs []runner.Header) []runner.Header {
	if m.r == nil {
		return hs
	}
	out := make([]runner.Header, len(hs))
	for i, h := range hs {
		out[i] = runner.Header{Key: m.string(h.Key), Value: m.string(h.Value)}
	}
	return out
}
