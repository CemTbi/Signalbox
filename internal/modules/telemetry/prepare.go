// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Limits on what one span keeps, as OpenTelemetry's SDKs default to. Past
// them, ingest drops the rest and counts what it dropped.
const (
	MaxAttributes     = 128
	MaxAttributeValue = 4 << 10
	MaxEvents         = 128
	MaxLinks          = 128
	maxNameLength     = 1 << 10
	// MaxOperations is how many operations a service keeps in the
	// per-minute rollup; the rest fold into OtherOperation.
	MaxOperations = 1000
)

// OtherOperation holds a service's operations past MaxOperations.
const OtherOperation = "(other)"

// Redacted replaces a credential's value.
const Redacted = "•••"

// UnknownService is a span's service when its resource doesn't name one,
// as OpenTelemetry's SDKs name it.
const UnknownService = "unknown_service"

// Preparer readies spans to be stored: it fills the fields copied from
// attributes, redacts credentials, enforces the limits and names
// operations. It is safe for concurrent use.
type Preparer struct {
	mu sync.Mutex
	// operations are the operations each service has used, by environment
	// and service, until MaxOperations.
	operations map[serviceKey]map[string]struct{}
}

type serviceKey struct {
	environment uuid.UUID
	service     string
}

// NewPreparer returns a Preparer.
func NewPreparer() *Preparer {
	return &Preparer{operations: map[serviceKey]map[string]struct{}{}}
}

// Dropped counts what Prepare left out of a span.
type Dropped struct {
	Attributes, Events, Links int
	// Truncated counts values cut to MaxAttributeValue.
	Truncated int
}

// Add adds o's counts.
func (d *Dropped) Add(o Dropped) {
	d.Attributes += o.Attributes
	d.Events += o.Events
	d.Links += o.Links
	d.Truncated += o.Truncated
}

// Prepare readies a span to be stored, and says what it left out.
func (p *Preparer) Prepare(s *Span) Dropped {
	var d Dropped
	s.Name = truncate(s.Name, maxNameLength)
	s.StatusMessage = truncate(s.StatusMessage, MaxAttributeValue)
	if s.Service = truncate(s.Service, maxNameLength); s.Service == "" {
		s.Service = UnknownService
	}
	s.ResourceAttributes = limitAttributes(s.ResourceAttributes, &d)
	s.Attributes = limitAttributes(s.Attributes, &d)
	if len(s.Events) > MaxEvents {
		d.Events += len(s.Events) - MaxEvents
		s.Events = s.Events[:MaxEvents]
	}
	for i := range s.Events {
		s.Events[i].Name = truncate(s.Events[i].Name, maxNameLength)
		s.Events[i].Attributes = limitAttributes(s.Events[i].Attributes, &d)
	}
	if len(s.Links) > MaxLinks {
		d.Links += len(s.Links) - MaxLinks
		s.Links = s.Links[:MaxLinks]
	}
	for i := range s.Links {
		s.Links[i].Attributes = limitAttributes(s.Links[i].Attributes, &d)
	}
	fillPromoted(s)
	s.Operation = p.operation(s)
	return d
}

// limitAttributes redacts credentials, keeps at most MaxAttributes, by key
// order so the same span keeps the same ones, and cuts long values.
func limitAttributes(attrs map[string]string, d *Dropped) map[string]string {
	if len(attrs) > MaxAttributes {
		keys := make([]string, 0, len(attrs))
		for k := range attrs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys[MaxAttributes:] {
			delete(attrs, k)
		}
		d.Attributes += len(keys) - MaxAttributes
	}
	for k, v := range attrs {
		switch {
		case isCredential(k):
			v = Redacted
		case urlAttributes[k]:
			v = redactQuery(v)
		}
		if len(v) > MaxAttributeValue {
			v = truncate(v, MaxAttributeValue)
			d.Truncated++
		}
		if len(k) > maxNameLength {
			delete(attrs, k)
			d.Attributes++
			continue
		}
		attrs[k] = v
	}
	return attrs
}

// truncate cuts s to at most n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// credentialWords are the words that mark an attribute, header or query
// parameter as holding a credential, such as http.request.header.authorization,
// set-cookie or access_token. Whole words only: input_tokens counts tokens
// and stays.
var credentialWords = map[string]bool{
	"authorization": true, "auth": true, "cookie": true, "token": true, "secret": true,
	"password": true, "passwd": true, "pwd": true, "apikey": true, "credential": true,
	"credentials": true, "signature": true, "sig": true,
}

func words(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == ' ' })
}

// isCredential reports whether a name marks a credential: one of the words
// above, or "api" followed by "key".
func isCredential(name string) bool {
	w := words(name)
	for i, word := range w {
		if credentialWords[word] || word == "api" && i+1 < len(w) && w[i+1] == "key" {
			return true
		}
	}
	return false
}

// urlAttributes hold URLs or their query, whose credentials are redacted
// parameter by parameter.
var urlAttributes = map[string]bool{
	"url.full": true, "url.query": true, "http.url": true, "http.target": true, "url.original": true,
}

// redactQuery replaces the values of a URL's query parameters that hold
// credentials. A parameter named key counts too, as API keys often are.
func redactQuery(v string) string {
	base, query, ok := strings.Cut(v, "?")
	if !ok {
		// url.query has no question mark.
		if strings.Contains(v, "=") && !strings.Contains(v, "/") {
			base, query = "", v
		} else {
			return v
		}
	}
	query, fragment, hasFragment := strings.Cut(query, "#")
	parts := strings.Split(query, "&")
	changed := false
	for i, part := range parts {
		name, _, hasValue := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if hasValue && (isCredential(decoded) || strings.EqualFold(decoded, "key")) {
			parts[i] = name + "=" + Redacted
			changed = true
		}
	}
	if !changed {
		return v
	}
	out := strings.Join(parts, "&")
	if ok {
		out = base + "?" + out
	}
	if hasFragment {
		out += "#" + fragment
	}
	return out
}

// promotedSources lists, for each field copied from attributes, the
// attribute names it comes from: the current semantic conventions first,
// then the names older instrumentation still sends.
var promotedSources = []struct {
	names []string
	set   func(s *Span, v string)
}{
	{[]string{"http.request.method", "http.method"}, func(s *Span, v string) { s.HTTPMethod = v }},
	{[]string{"http.route"}, func(s *Span, v string) { s.HTTPRoute = v }},
	{[]string{"http.response.status_code", "http.status_code"}, func(s *Span, v string) {
		if n, err := strconv.ParseUint(v, 10, 16); err == nil {
			s.HTTPStatusCode = uint16(n)
		}
	}},
	{[]string{"server.address", "net.peer.name"}, func(s *Span, v string) { s.ServerAddress = v }},
	{[]string{"db.system.name", "db.system"}, func(s *Span, v string) { s.DBSystem = v }},
}

// fillPromoted sets the fields copied from attributes that are still
// empty.
func fillPromoted(s *Span) {
	for _, p := range promotedSources {
		for _, name := range p.names {
			if v := s.Attributes[name]; v != "" {
				p.set(s, v)
				break
			}
		}
	}
}

// operation names what the rollup groups the span by.
func (p *Preparer) operation(s *Span) string {
	op := s.Name
	if s.Kind == KindServer && s.HTTPRoute != "" {
		op = strings.TrimSpace(s.HTTPMethod + " " + s.HTTPRoute)
	}
	key := serviceKey{s.EnvironmentID, s.Service}
	p.mu.Lock()
	defer p.mu.Unlock()
	ops, ok := p.operations[key]
	if !ok {
		// Services are few; this bounds memory if they aren't.
		if len(p.operations) >= 100_000 {
			clear(p.operations)
		}
		ops = map[string]struct{}{}
		p.operations[key] = ops
	}
	if _, seen := ops[op]; seen {
		return op
	}
	if len(ops) >= MaxOperations {
		return OtherOperation
	}
	ops[op] = struct{}{}
	return op
}
