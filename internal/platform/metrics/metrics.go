// SPDX-License-Identifier: Apache-2.0

// Package metrics counts what the server does and serves the counts in the
// Prometheus text exposition format, version 0.0.4, which Prometheus,
// VictoriaMetrics, Grafana Alloy and the OpenTelemetry Collector scrape.
// See docs/adr/0038-metrics.md.
//
// Metrics are made from a Registry. A nil Registry makes nil metrics, and
// every method of a nil metric does nothing, so a module counts the same
// way whether or not the process exports what it counts.
package metrics

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// MediaType is the media type of the text exposition format.
const MediaType = "text/plain; version=0.0.4; charset=utf-8"

// MaxSeries bounds the label combinations one metric keeps. Values past it
// are counted in one series whose labels are all Overflow, so a bug that
// labels by something unbounded can't exhaust memory, and shows.
const MaxSeries = 1000

// Overflow is every label's value in the series that counts the label
// combinations past MaxSeries.
const Overflow = "_overflow"

// DurationBuckets are histogram buckets for durations in seconds, from 5 ms
// to a minute.
var DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

var (
	validName  = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	validLabel = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// Registry holds metrics and serves them. It is safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	families map[string]family
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{families: map[string]family{}}
}

type family interface {
	// write appends the family's samples in the exposition format.
	write(b *bytes.Buffer)
}

type header struct {
	name, help, kind string
}

func (h header) write(b *bytes.Buffer) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", h.name, escapeHelp(h.help), h.name, h.kind)
}

// register adds a family. A bad name or a name used twice is a programming
// error, found by the first test that builds the registry, so it panics.
func (r *Registry) register(name string, labels []string, f family) {
	if !validName.MatchString(name) {
		panic(fmt.Sprintf("metrics: %q isn't a valid metric name", name))
	}
	for _, l := range labels {
		if !validLabel.MatchString(l) || strings.HasPrefix(l, "__") || l == "le" {
			panic(fmt.Sprintf("metrics: %q isn't a valid label name for %s", l, name))
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.families[name]; ok {
		panic(fmt.Sprintf("metrics: %s is registered twice", name))
	}
	r.families[name] = f
}

// Handler serves every metric, sorted by name.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var b bytes.Buffer
		r.WriteTo(&b)
		w.Header().Set("Content-Type", MediaType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b.Bytes())
	})
}

// WriteTo appends every metric to b in the exposition format.
func (r *Registry) WriteTo(b *bytes.Buffer) {
	r.mu.Lock()
	names := make([]string, 0, len(r.families))
	for name := range r.families {
		names = append(names, name)
	}
	fams := make([]family, len(names))
	slices.Sort(names)
	for i, name := range names {
		fams[i] = r.families[name]
	}
	r.mu.Unlock()
	for _, f := range fams {
		f.write(b)
	}
}

// series keeps one value per label combination.
type series[T any] struct {
	labels []string
	newT   func() *T

	mu   sync.RWMutex
	byID map[string]*entry[T]
}

type entry[T any] struct {
	values []string
	v      *T
}

func newSeries[T any](labels []string, newT func() *T) *series[T] {
	s := &series[T]{labels: slices.Clone(labels), newT: newT, byID: map[string]*entry[T]{}}
	if len(labels) == 0 {
		// A metric without labels has its one series from the start, so it
		// reads 0 before anything happens.
		s.get(nil)
	}
	return s
}

// get returns the value for a label combination, adding it if it's new.
// Passing the wrong number of values is a programming error.
func (s *series[T]) get(values []string) *T {
	if len(values) != len(s.labels) {
		panic(fmt.Sprintf("metrics: %d label values for labels %v", len(values), s.labels))
	}
	id := strings.Join(values, "\xff")
	s.mu.RLock()
	e, ok := s.byID[id]
	s.mu.RUnlock()
	if ok {
		return e.v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.byID[id]; ok {
		return e.v
	}
	if len(s.byID) >= MaxSeries {
		values = make([]string, len(s.labels))
		for i := range values {
			values[i] = Overflow
		}
		id = strings.Join(values, "\xff")
		if e, ok := s.byID[id]; ok {
			return e.v
		}
	}
	e = &entry[T]{values: slices.Clone(values), v: s.newT()}
	s.byID[id] = e
	return e.v
}

// each calls f for every series, ordered by label values.
func (s *series[T]) each(f func(values []string, v *T)) {
	s.mu.RLock()
	entries := make([]*entry[T], 0, len(s.byID))
	for _, e := range s.byID {
		entries = append(entries, e)
	}
	s.mu.RUnlock()
	slices.SortFunc(entries, func(a, b *entry[T]) int { return slices.Compare(a.values, b.values) })
	for _, e := range entries {
		f(e.values, e.v)
	}
}

// formatLabels writes {a="1",b="2"}, with an extra label when extraName
// isn't empty, or nothing when there are no labels.
func formatLabels(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escapeLabel(values[i]))
		b.WriteByte('"')
	}
	if extraName != "" {
		if len(names) > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extraName)
		b.WriteString(`="`)
		b.WriteString(extraValue)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// atomicFloat is a float64 that goroutines can change at once.
type atomicFloat struct{ bits atomic.Uint64 }

func (f *atomicFloat) load() float64   { return math.Float64frombits(f.bits.Load()) }
func (f *atomicFloat) store(v float64) { f.bits.Store(math.Float64bits(v)) }

func (f *atomicFloat) add(v float64) {
	for {
		old := f.bits.Load()
		if f.bits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+v)) {
			return
		}
	}
}

// Counter is a count that only goes up, such as requests handled. Its name
// ends in _total.
type Counter struct {
	header
	s *series[atomicFloat]
}

// NewCounter registers a counter with the given label names.
func (r *Registry) NewCounter(name, help string, labels ...string) *Counter {
	if r == nil {
		return nil
	}
	if !strings.HasSuffix(name, "_total") {
		panic(fmt.Sprintf("metrics: counter %s must end in _total", name))
	}
	c := &Counter{header{name, help, "counter"}, newSeries(labels, func() *atomicFloat { return new(atomicFloat) })}
	r.register(name, labels, c)
	return c
}

// Inc adds one to the series with the given label values.
func (c *Counter) Inc(values ...string) { c.Add(1, values...) }

// Add adds v, which must not be negative, to the series with the given
// label values.
func (c *Counter) Add(v float64, values ...string) {
	if c == nil {
		return
	}
	if v < 0 {
		panic(fmt.Sprintf("metrics: counter %s can't go down", c.name))
	}
	c.s.get(values).add(v)
}

func (c *Counter) write(b *bytes.Buffer) {
	c.header.write(b)
	c.s.each(func(values []string, v *atomicFloat) {
		fmt.Fprintf(b, "%s%s %s\n", c.name, formatLabels(c.s.labels, values, "", ""), formatFloat(v.load()))
	})
}

// Gauge is a value that goes up and down, such as requests in progress.
type Gauge struct {
	header
	s *series[atomicFloat]
}

// NewGauge registers a gauge with the given label names.
func (r *Registry) NewGauge(name, help string, labels ...string) *Gauge {
	if r == nil {
		return nil
	}
	g := &Gauge{header{name, help, "gauge"}, newSeries(labels, func() *atomicFloat { return new(atomicFloat) })}
	r.register(name, labels, g)
	return g
}

// Set sets the series with the given label values to v.
func (g *Gauge) Set(v float64, values ...string) {
	if g == nil {
		return
	}
	g.s.get(values).store(v)
}

// Add adds v, which may be negative, to the series with the given label
// values.
func (g *Gauge) Add(v float64, values ...string) {
	if g == nil {
		return
	}
	g.s.get(values).add(v)
}

// Inc adds one.
func (g *Gauge) Inc(values ...string) { g.Add(1, values...) }

// Dec subtracts one.
func (g *Gauge) Dec(values ...string) { g.Add(-1, values...) }

func (g *Gauge) write(b *bytes.Buffer) {
	g.header.write(b)
	g.s.each(func(values []string, v *atomicFloat) {
		fmt.Fprintf(b, "%s%s %s\n", g.name, formatLabels(g.s.labels, values, "", ""), formatFloat(v.load()))
	})
}

// funcMetric reads its one value when it's scraped.
type funcMetric struct {
	header
	f func() float64
}

func (m *funcMetric) write(b *bytes.Buffer) {
	m.header.write(b)
	fmt.Fprintf(b, "%s %s\n", m.name, formatFloat(m.f()))
}

// NewGaugeFunc registers a gauge whose value f reads at each scrape, such
// as the size of a pool. f must be quick and safe for concurrent use.
func (r *Registry) NewGaugeFunc(name, help string, f func() float64) {
	if r == nil {
		return
	}
	r.register(name, nil, &funcMetric{header{name, help, "gauge"}, f})
}

// NewCounterFunc registers a counter whose value f reads at each scrape,
// from a count kept elsewhere that only goes up.
func (r *Registry) NewCounterFunc(name, help string, f func() float64) {
	if r == nil {
		return
	}
	if !strings.HasSuffix(name, "_total") {
		panic(fmt.Sprintf("metrics: counter %s must end in _total", name))
	}
	r.register(name, nil, &funcMetric{header{name, help, "counter"}, f})
}

// Histogram counts observations, such as durations, in buckets, and keeps
// their sum and count.
type Histogram struct {
	header
	buckets []float64
	s       *series[histogramValue]
}

type histogramValue struct {
	// counts holds each bucket's own count, the last for values above
	// every bucket. Each scrape adds them up, so the +Inf bucket and the
	// count always agree.
	counts []atomic.Uint64
	sum    atomicFloat
}

// NewHistogram registers a histogram with the given upper bounds, in
// increasing order, and label names.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	if r == nil {
		return nil
	}
	if len(buckets) == 0 {
		panic(fmt.Sprintf("metrics: histogram %s needs buckets", name))
	}
	for i, upper := range buckets {
		if math.IsInf(upper, 0) || math.IsNaN(upper) || i > 0 && upper <= buckets[i-1] {
			panic(fmt.Sprintf("metrics: histogram %s needs finite buckets in increasing order", name))
		}
	}
	h := &Histogram{header: header{name, help, "histogram"}, buckets: slices.Clone(buckets)}
	h.s = newSeries(labels, func() *histogramValue { return &histogramValue{counts: make([]atomic.Uint64, len(buckets)+1)} })
	r.register(name, labels, h)
	return h
}

// Observe records v in the series with the given label values.
func (h *Histogram) Observe(v float64, values ...string) {
	if h == nil {
		return
	}
	hv := h.s.get(values)
	i, _ := slices.BinarySearch(h.buckets, v)
	hv.counts[i].Add(1)
	hv.sum.add(v)
}

func (h *Histogram) write(b *bytes.Buffer) {
	h.header.write(b)
	names := h.s.labels
	h.s.each(func(values []string, v *histogramValue) {
		var total uint64
		for i, upper := range h.buckets {
			total += v.counts[i].Load()
			fmt.Fprintf(b, "%s_bucket%s %d\n", h.name, formatLabels(names, values, "le", formatFloat(upper)), total)
		}
		total += v.counts[len(h.buckets)].Load()
		fmt.Fprintf(b, "%s_bucket%s %d\n", h.name, formatLabels(names, values, "le", "+Inf"), total)
		labels := formatLabels(names, values, "", "")
		fmt.Fprintf(b, "%s_sum%s %s\n", h.name, labels, formatFloat(v.sum.load()))
		fmt.Fprintf(b, "%s_count%s %d\n", h.name, labels, total)
	})
}
