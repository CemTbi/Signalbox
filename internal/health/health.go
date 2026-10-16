// SPDX-License-Identifier: Apache-2.0

// Package health serves liveness and readiness in the Health Check Response
// Format for HTTP APIs (draft-inadarei-api-health-check), the same format
// Signalbox's own health monitoring reads.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MediaType is the media type of health responses.
const MediaType = "application/health+json"

// Status is a health state: pass, warn or fail.
type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Check reports on one component the service depends on.
type Check struct {
	// Name is the key in the response, in "component:measurement" form,
	// such as "postgres:responseTime".
	Name string
	// ComponentType is "datastore", "system" or "component".
	ComponentType string
	// Critical checks fail the whole service when they fail. A failing
	// non-critical check only downgrades it to warn.
	Critical bool
	Run      func(ctx context.Context) Result
}

// Result is the outcome of one run of a check.
type Result struct {
	Status        Status
	ObservedValue any
	ObservedUnit  string
	// Output explains a warn or fail result.
	Output string
}

// Response is a health+json document.
type Response struct {
	Status    Status                   `json:"status"`
	Version   string                   `json:"version,omitempty"`
	ReleaseID string                   `json:"releaseId,omitempty"`
	ServiceID string                   `json:"serviceId,omitempty"`
	Output    string                   `json:"output,omitempty"`
	Checks    map[string][]CheckResult `json:"checks,omitempty"`
}

// CheckResult is one entry under "checks".
type CheckResult struct {
	ComponentType string `json:"componentType,omitempty"`
	ObservedValue any    `json:"observedValue,omitempty"`
	ObservedUnit  string `json:"observedUnit,omitempty"`
	Status        Status `json:"status"`
	Time          string `json:"time"`
	Output        string `json:"output,omitempty"`
}

// Checker runs the registered checks and serves the results.
type Checker struct {
	serviceID string
	releaseID string
	timeout   time.Duration
	now       func() time.Time

	mu       sync.RWMutex
	checks   []Check
	draining atomic.Bool
}

// New returns a Checker for the given service and release.
func New(serviceID, releaseID string) *Checker {
	return &Checker{serviceID: serviceID, releaseID: releaseID, timeout: 2 * time.Second, now: time.Now}
}

// Add registers a check. Checks run on every readiness request.
func (c *Checker) Add(check Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks = append(c.checks, check)
}

// SetDraining marks the service as shutting down. Readiness then fails at
// once, so load balancers stop sending traffic before listeners close.
func (c *Checker) SetDraining() { c.draining.Store(true) }

// Live reports that the process is running. It never runs checks, so a slow
// database can't make an orchestrator restart a healthy process.
func (c *Checker) Live() Response {
	return Response{Status: Pass, Version: "1", ReleaseID: c.releaseID, ServiceID: c.serviceID}
}

// Ready runs every check at the same time, each with its own timeout, and
// combines the results.
func (c *Checker) Ready(ctx context.Context) Response {
	resp := c.Live()
	if c.draining.Load() {
		resp.Status = Fail
		resp.Output = "shutting down"
		return resp
	}

	c.mu.RLock()
	checks := append([]Check(nil), c.checks...)
	c.mu.RUnlock()

	results := make([]Result, len(checks))
	var wg sync.WaitGroup
	for i, check := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.run(ctx, check)
		}()
	}
	wg.Wait()

	resp.Checks = map[string][]CheckResult{}
	at := c.now().UTC().Format(time.RFC3339)
	for i, check := range checks {
		r := results[i]
		resp.Checks[check.Name] = append(resp.Checks[check.Name], CheckResult{
			ComponentType: check.ComponentType,
			ObservedValue: r.ObservedValue,
			ObservedUnit:  r.ObservedUnit,
			Status:        r.Status,
			Time:          at,
			Output:        r.Output,
		})
		resp.Status = worse(resp.Status, effective(check, r.Status))
	}
	if resp.Status != Pass {
		resp.Output = failingSummary(checks, results)
	}
	return resp
}

func (c *Checker) run(ctx context.Context, check Check) Result {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		// recover must run in the goroutine that calls the check.
		defer func() {
			if p := recover(); p != nil {
				done <- Result{Status: Fail, Output: fmt.Sprintf("check panicked: %v", p)}
			}
		}()
		done <- check.Run(ctx)
	}()
	select {
	case r := <-done:
		if r.Status == "" {
			r.Status = Pass
		}
		return r
	case <-ctx.Done():
		return Result{Status: Fail, Output: fmt.Sprintf("no answer within %s", c.timeout)}
	}
}

// effective downgrades a non-critical failure to a warning.
func effective(check Check, s Status) Status {
	if s == Fail && !check.Critical {
		return Warn
	}
	return s
}

func worse(a, b Status) Status {
	rank := map[Status]int{Pass: 0, Warn: 1, Fail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func failingSummary(checks []Check, results []Result) string {
	var names []string
	for i, check := range checks {
		if results[i].Status != Pass {
			names = append(names, check.Name)
		}
	}
	sort.Strings(names)
	return fmt.Sprintf("checks not passing: %v", names)
}

// LiveHandler serves Live.
func (c *Checker) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		write(w, c.Live())
	})
}

// ReadyHandler serves Ready.
func (c *Checker) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write(w, c.Ready(r.Context()))
	})
}

func write(w http.ResponseWriter, resp Response) {
	status := http.StatusOK
	if resp.Status == Fail {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", MediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// Uptime is a check that always passes and reports how long the process has
// been running.
func Uptime(started time.Time) Check {
	return Check{
		Name:          "uptime",
		ComponentType: "system",
		Run: func(context.Context) Result {
			return Result{Status: Pass, ObservedValue: int64(time.Since(started).Seconds()), ObservedUnit: "s"}
		},
	}
}
