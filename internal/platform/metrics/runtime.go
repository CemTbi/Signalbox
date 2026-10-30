// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"runtime"
	rtmetrics "runtime/metrics"
	"time"
)

// RegisterRuntime adds the Go runtime's goroutines, memory and garbage
// collections, under the names the Prometheus Go client uses, so existing
// Go dashboards work, and when the process started.
func (r *Registry) RegisterRuntime(started time.Time) {
	if r == nil {
		return
	}
	r.NewGaugeFunc("go_goroutines", "Goroutines that currently exist.", runtimeValue("/sched/goroutines:goroutines"))
	r.NewGaugeFunc("go_sched_gomaxprocs_threads", "The current runtime.GOMAXPROCS setting.", runtimeValue("/sched/gomaxprocs:threads"))
	r.NewGaugeFunc("go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", runtimeValue("/memory/classes/heap/objects:bytes"))
	r.NewGaugeFunc("go_memstats_sys_bytes", "Bytes of memory the Go runtime obtained from the system.", runtimeValue("/memory/classes/total:bytes"))
	r.NewGaugeFunc("go_gc_gogc_percent", "The heap growth that triggers a garbage collection, as GOGC sets it.", runtimeValue("/gc/gogc:percent"))
	r.NewCounterFunc("go_gc_cycles_total", "Completed garbage collection cycles.", runtimeValue("/gc/cycles/total:gc-cycles"))
	g := r.NewGauge("go_info", "Information about the Go runtime.", "version")
	g.Set(1, runtime.Version())
	start := float64(started.UnixNano()) / 1e9
	r.NewGaugeFunc("process_start_time_seconds", "When the process started, in seconds since the Unix epoch.", func() float64 { return start })
}

// runtimeValue reads one of the runtime's metrics.
func runtimeValue(name string) func() float64 {
	return func() float64 {
		s := []rtmetrics.Sample{{Name: name}}
		rtmetrics.Read(s)
		switch s[0].Value.Kind() {
		case rtmetrics.KindUint64:
			return float64(s[0].Value.Uint64())
		case rtmetrics.KindFloat64:
			return s[0].Value.Float64()
		}
		return 0
	}
}
