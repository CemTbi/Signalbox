// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
)

// A health component's status, whatever words its format uses.
const (
	ComponentOK   = "ok"
	ComponentWarn = "warn"
	ComponentDown = "down"
)

const (
	maxComponents      = 50
	maxComponentName   = 200
	maxComponentDetail = 300
)

// Component is one part of a service, as its health check reports it.
type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// healthReport is what a health response says. Its overall status is "" when
// the body isn't one of the formats read.
type healthReport struct {
	overall    string
	components []Component
}

func (h healthReport) anyDown() bool {
	for _, c := range h.components {
		if c.Status == ComponentDown {
			return true
		}
	}
	return false
}

// summary is lead, then the components that aren't well.
func (h healthReport) summary(lead string) string {
	var bad []string
	for _, c := range h.components {
		if c.Status != ComponentOK {
			bad = append(bad, c.Name+" is "+c.Status)
		}
	}
	if len(bad) == 0 {
		return lead + "."
	}
	return lead + ": " + strings.Join(bad, ", ") + "."
}

// readHealth reads application/health+json, Spring Boot Actuator's health or
// Kubernetes' verbose /readyz. Any other body says nothing about health, and
// the check rests on its status and assertions.
func readHealth(r *runner.Response) healthReport {
	if text := strings.TrimSpace(string(r.Body)); strings.HasPrefix(text, "[+]") || strings.HasPrefix(text, "[-]") {
		return readKubernetes(text)
	}
	var doc struct {
		Status     string                       `json:"status"`
		Output     string                       `json:"output"`
		Checks     map[string][]json.RawMessage `json:"checks"`
		Components map[string]json.RawMessage   `json:"components"`
		Details    map[string]json.RawMessage   `json:"details"`
	}
	if err := json.Unmarshal(r.Body, &doc); err != nil {
		return healthReport{}
	}
	overall := statusOf(doc.Status)
	if overall == "" {
		return healthReport{}
	}
	out := healthReport{overall: overall}
	// RFC draft inadvisable-health-check: each check names an array, one
	// entry per instance.
	for name, entries := range doc.Checks {
		for _, raw := range entries {
			var c struct {
				ComponentID string `json:"componentId"`
				Status      string `json:"status"`
				Output      string `json:"output"`
			}
			if json.Unmarshal(raw, &c) != nil {
				continue
			}
			label := name
			if c.ComponentID != "" && !strings.HasPrefix(name, c.ComponentID) {
				label = c.ComponentID + ":" + name
			}
			out.add(label, c.Status, c.Output)
		}
	}
	// Spring Boot: components, or details before 2.2.
	parts := doc.Components
	if len(parts) == 0 {
		parts = doc.Details
	}
	for name, raw := range parts {
		var c struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &c) == nil {
			out.add(name, c.Status, "")
		}
	}
	sort.Slice(out.components, func(i, j int) bool { return out.components[i].Name < out.components[j].Name })
	if len(out.components) > maxComponents {
		out.components = out.components[:maxComponents]
	}
	return out
}

func (h *healthReport) add(name, status, detail string) {
	s := statusOf(status)
	if s == "" {
		return
	}
	h.components = append(h.components, Component{Name: clip(name, maxComponentName), Status: s, Detail: clip(detail, maxComponentDetail)})
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// statusOf maps the words of the health formats, or "" for others. Spring
// Boot's OUT_OF_SERVICE and UNKNOWN count: one is down, and the other warns.
func statusOf(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass", "ok", "up":
		return ComponentOK
	case "warn", "unknown":
		return ComponentWarn
	case "fail", "error", "down", "out_of_service":
		return ComponentDown
	}
	return ""
}

// readKubernetes reads lines such as "[+]ping ok" and "[-]etcd failed:
// reason withheld", which end with "readyz check passed" or "... failed".
func readKubernetes(text string) healthReport {
	out := healthReport{overall: ComponentOK}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[+]"):
			name, _, _ := strings.Cut(line[3:], " ")
			out.add(name, ComponentOK, "")
		case strings.HasPrefix(line, "[-]"):
			name, detail, _ := strings.Cut(line[3:], " ")
			out.add(name, ComponentDown, detail)
			out.overall = ComponentDown
		}
		if len(out.components) >= maxComponents {
			break
		}
	}
	return out
}
