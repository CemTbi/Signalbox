// SPDX-License-Identifier: Apache-2.0

package metrics_test

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

func scrape(t *testing.T, r *metrics.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != metrics.MediaType {
		t.Errorf("Content-Type = %q", got)
	}
	return rec.Body.String()
}

func TestExpositionFormat(t *testing.T) {
	r := metrics.NewRegistry()
	c := r.NewCounter("sbx_requests_total", "Requests handled.\nBy route.", "route", "code")
	g := r.NewGauge("sbx_in_progress", "Requests in progress.")
	h := r.NewHistogram("sbx_duration_seconds", `How long a request took, in "seconds".`, []float64{0.1, 1}, "route")
	r.NewGaugeFunc("sbx_pool_size", "Connections in the pool.", func() float64 { return 3 })
	r.NewCounterFunc("sbx_acquires_total", "Connections taken from the pool.", func() float64 { return 1.5 })
	r.NewGauge("sbx_unused", "A labelled gauge nobody set.", "kind")

	c.Inc("/b", "200")
	c.Add(2, "/a", "500")
	c.Inc("/a", "200")
	c.Inc(`we"ird\path`+"\n", "404")
	g.Inc()
	g.Inc()
	g.Dec()
	h.Observe(0.05, "/a")
	h.Observe(0.1, "/a") // a bucket's bound is inside it
	h.Observe(0.5, "/a")
	h.Observe(7, "/a")

	want := `# HELP sbx_acquires_total Connections taken from the pool.
# TYPE sbx_acquires_total counter
sbx_acquires_total 1.5
# HELP sbx_duration_seconds How long a request took, in "seconds".
# TYPE sbx_duration_seconds histogram
sbx_duration_seconds_bucket{route="/a",le="0.1"} 2
sbx_duration_seconds_bucket{route="/a",le="1"} 3
sbx_duration_seconds_bucket{route="/a",le="+Inf"} 4
sbx_duration_seconds_sum{route="/a"} 7.65
sbx_duration_seconds_count{route="/a"} 4
# HELP sbx_in_progress Requests in progress.
# TYPE sbx_in_progress gauge
sbx_in_progress 1
# HELP sbx_pool_size Connections in the pool.
# TYPE sbx_pool_size gauge
sbx_pool_size 3
# HELP sbx_requests_total Requests handled.\nBy route.
# TYPE sbx_requests_total counter
sbx_requests_total{route="/a",code="200"} 1
sbx_requests_total{route="/a",code="500"} 2
sbx_requests_total{route="/b",code="200"} 1
sbx_requests_total{route="we\"ird\\path\n",code="404"} 1
# HELP sbx_unused A labelled gauge nobody set.
# TYPE sbx_unused gauge
`
	if got := scrape(t, r); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSpecialValues(t *testing.T) {
	r := metrics.NewRegistry()
	g := r.NewGauge("sbx_value", "A value.", "which")
	g.Set(math.Inf(1), "inf")
	g.Set(math.Inf(-1), "minus-inf")
	g.Set(math.NaN(), "nan")
	g.Set(1e21, "big")
	got := scrape(t, r)
	for _, want := range []string{`{which="inf"} +Inf`, `{which="minus-inf"} -Inf`, `{which="nan"} NaN`, `{which="big"} 1e+21`} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %s:\n%s", want, got)
		}
	}
}

func TestNilMetricsDoNothing(t *testing.T) {
	var r *metrics.Registry
	c := r.NewCounter("sbx_a_total", "A.", "x")
	g := r.NewGauge("sbx_b", "B.")
	h := r.NewHistogram("sbx_c_seconds", "C.", metrics.DurationBuckets)
	r.NewGaugeFunc("sbx_d", "D.", func() float64 { return 0 })
	r.NewCounterFunc("sbx_e_total", "E.", func() float64 { return 0 })
	r.RegisterRuntime(time.Now())
	c.Inc("1")
	c.Add(2, "1")
	g.Set(1)
	g.Inc()
	g.Dec()
	h.Observe(1)
}

func TestOverflowSeries(t *testing.T) {
	r := metrics.NewRegistry()
	c := r.NewCounter("sbx_paths_total", "Paths.", "path", "code")
	for i := range metrics.MaxSeries + 10 {
		c.Inc(fmt.Sprint("/p", i), "200")
	}
	got := scrape(t, r)
	if n := strings.Count(got, "sbx_paths_total{"); n != metrics.MaxSeries+1 {
		t.Errorf("%d series, want %d", n, metrics.MaxSeries+1)
	}
	if !strings.Contains(got, `sbx_paths_total{path="_overflow",code="_overflow"} 10`) {
		t.Error("the overflow series doesn't count the rest")
	}
	// Combinations seen before the limit keep their own series.
	c.Inc("/p0", "200")
	if !strings.Contains(scrape(t, r), `sbx_paths_total{path="/p0",code="200"} 2`) {
		t.Error("an existing series moved to the overflow")
	}
}

func TestConcurrentUpdates(t *testing.T) {
	r := metrics.NewRegistry()
	c := r.NewCounter("sbx_hits_total", "Hits.", "worker")
	g := r.NewGauge("sbx_level", "Level.")
	h := r.NewHistogram("sbx_size", "Size.", []float64{1, 10})
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for range 1000 {
				c.Inc(fmt.Sprint(w % 2))
				g.Add(0.5)
				h.Observe(5)
			}
		})
	}
	// Scraping while others write must be safe.
	wg.Go(func() {
		for range 20 {
			var b bytes.Buffer
			r.WriteTo(&b)
		}
	})
	wg.Wait()
	got := scrape(t, r)
	for _, want := range []string{`sbx_hits_total{worker="0"} 4000`, `sbx_hits_total{worker="1"} 4000`, "sbx_level 4000", "sbx_size_count 8000", "sbx_size_sum 40000"} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("lacks %s:\n%s", want, got)
		}
	}
}

func TestProgrammingErrorsPanic(t *testing.T) {
	for name, f := range map[string]func(r *metrics.Registry){
		"bad name":            func(r *metrics.Registry) { r.NewGauge("sbx-value", "") },
		"bad label":           func(r *metrics.Registry) { r.NewGauge("sbx_value", "", "a-b") },
		"reserved label":      func(r *metrics.Registry) { r.NewGauge("sbx_value", "", "__name") },
		"le label":            func(r *metrics.Registry) { r.NewHistogram("sbx_value", "", []float64{1}, "le") },
		"counter name":        func(r *metrics.Registry) { r.NewCounter("sbx_requests", "") },
		"counter func name":   func(r *metrics.Registry) { r.NewCounterFunc("sbx_requests", "", func() float64 { return 0 }) },
		"twice":               func(r *metrics.Registry) { r.NewGauge("sbx_value", ""); r.NewGauge("sbx_value", "") },
		"unsorted buckets":    func(r *metrics.Registry) { r.NewHistogram("sbx_value", "", []float64{2, 1}) },
		"repeated bucket":     func(r *metrics.Registry) { r.NewHistogram("sbx_value", "", []float64{1, 1}) },
		"infinite bucket":     func(r *metrics.Registry) { r.NewHistogram("sbx_value", "", []float64{1, math.Inf(1)}) },
		"no buckets":          func(r *metrics.Registry) { r.NewHistogram("sbx_value", "", nil) },
		"wrong label count":   func(r *metrics.Registry) { r.NewGauge("sbx_value", "", "a").Set(1) },
		"negative count":      func(r *metrics.Registry) { r.NewCounter("sbx_value_total", "").Add(-1) },
		"label on unlabelled": func(r *metrics.Registry) { r.NewCounter("sbx_value_total", "").Inc("x") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("didn't panic")
				}
			}()
			f(metrics.NewRegistry())
		})
	}
}

func TestRuntimeMetrics(t *testing.T) {
	r := metrics.NewRegistry()
	started := time.Unix(1700000000, 500_000_000)
	r.RegisterRuntime(started)
	got := scrape(t, r)
	for _, want := range []string{
		"# TYPE go_goroutines gauge\ngo_goroutines ",
		"# TYPE go_gc_cycles_total counter\n",
		"go_memstats_heap_alloc_bytes ",
		"go_memstats_sys_bytes ",
		`go_info{version="go`,
		"process_start_time_seconds 1.7000000005e+09\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "go_goroutines 0\n") || strings.Contains(got, "go_memstats_sys_bytes 0\n") {
		t.Errorf("runtime values read as 0:\n%s", got)
	}
}
