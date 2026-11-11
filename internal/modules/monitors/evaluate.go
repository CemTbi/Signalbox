// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/speakeasy-api/jsonpath/pkg/jsonpath"
	"gopkg.in/yaml.v3"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// The outcomes of a check.
const (
	OutcomePassed   = "passed"
	OutcomeDegraded = "degraded"
	OutcomeFailed   = "failed"
	// OutcomeSkipped means the check didn't run: the one before it was still
	// running, or Signalbox couldn't send it. It says nothing of the target.
	OutcomeSkipped = "skipped"
)

// What can be asserted about a response.
const (
	AssertStatus       = "status"
	AssertHeader       = "header"
	AssertJSONPath     = "jsonPath"
	AssertResponseTime = "responseTime"
)

// How a value is compared.
const (
	OpEquals      = "equals"
	OpNotEquals   = "notEquals"
	OpContains    = "contains"
	OpLessThan    = "lessThan"
	OpGreaterThan = "greaterThan"
	OpExists      = "exists"
	OpNotExists   = "notExists"
)

// maxAssertedBody bounds the body a JSONPath assertion reads, since the
// document is parsed whole on every check.
const maxAssertedBody = 1 << 20

// Assertion is one thing a check's response must satisfy.
type Assertion struct {
	Kind string `json:"kind"`
	// Target is the header's name, or the JSONPath.
	Target string `json:"target,omitempty"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
}

var opsOf = map[string][]string{
	AssertStatus:       {OpEquals, OpNotEquals, OpLessThan, OpGreaterThan},
	AssertHeader:       {OpEquals, OpNotEquals, OpContains, OpExists, OpNotExists},
	AssertJSONPath:     {OpEquals, OpNotEquals, OpContains, OpLessThan, OpGreaterThan, OpExists, OpNotExists},
	AssertResponseTime: {OpLessThan},
}

func checkAssertion(errs *validate.Errors, pointer string, a *Assertion) {
	ops, ok := opsOf[a.Kind]
	if !ok {
		errs.Add(pointer+"/kind", "enum", "Use status, header, jsonPath or responseTime.")
		return
	}
	known := false
	for _, op := range ops {
		known = known || op == a.Op
	}
	if !known {
		errs.Add(pointer+"/op", "enum", fmt.Sprintf("Use one of: %s.", strings.Join(ops, ", ")))
		return
	}
	switch a.Kind {
	case AssertHeader:
		a.Target = strings.TrimSpace(a.Target)
		if a.Target == "" || len(a.Target) > 1000 {
			errs.Add(pointer+"/target", "required", "Name the header, in at most 1,000 characters.")
		}
	case AssertJSONPath:
		a.Target = strings.TrimSpace(a.Target)
		if len(a.Target) > 1000 {
			errs.Add(pointer+"/target", "maxLength", "Use a JSONPath of at most 1,000 characters.")
		} else if _, err := jsonpath.NewPath(a.Target); err != nil || a.Target == "" {
			errs.Add(pointer+"/target", "format", "Use an RFC 9535 JSONPath, such as $.status.")
		}
	default:
		a.Target = ""
	}
	if len(a.Value) > 4096 {
		errs.Add(pointer+"/value", "maxLength", "Use a value of at most 4,096 characters.")
	}
	switch {
	case a.Op == OpExists || a.Op == OpNotExists:
		a.Value = ""
	case a.Kind == AssertStatus || a.Kind == AssertResponseTime || a.Op == OpLessThan || a.Op == OpGreaterThan:
		if _, err := strconv.ParseFloat(strings.TrimSpace(a.Value), 64); err != nil {
			errs.Add(pointer+"/value", "type", "Use a number.")
		}
	}
}

// Check is what one check found. Its messages come from a masked response,
// so they hold no secrets.
type Check struct {
	MonitorID, EnvironmentID uuid.UUID
	At                       time.Time
	Outcome                  string
	Duration                 time.Duration
	// Status is 0 when no response came.
	Status         int
	FailureCode    string
	FailureMessage string
	Components     []Component
	// CertificateExpiresAt is zero when the response didn't come over TLS.
	CertificateExpiresAt time.Time
	TraceID              string
	// RunID is a collection monitor's test run; uuid.Nil for others.
	RunID uuid.UUID
}

// evaluate decides a check's outcome from what came back.
func evaluate(mon *Monitor, e client.Entry, now time.Time) Check {
	c := Check{
		MonitorID: mon.ID, EnvironmentID: mon.EnvironmentID, At: now, Outcome: OutcomePassed,
		Duration: e.Result.Timings.Total, TraceID: e.TraceID,
	}
	if f := e.Result.Failure; f != nil {
		c.Outcome, c.FailureCode, c.FailureMessage = OutcomeFailed, f.Code, f.Message
		return c
	}
	r := e.Result.Response
	if r == nil {
		c.Outcome, c.FailureCode, c.FailureMessage = OutcomeFailed, "network", "The request got no response."
		return c
	}
	c.Status, c.CertificateExpiresAt = r.Status, r.CertificateExpiresAt

	var report healthReport
	if mon.Kind == KindHealth {
		report = readHealth(r)
		c.Components = report.components
	}
	if msg := firstFailure(mon.Assertions, r, c.Duration); msg != "" {
		c.Outcome, c.FailureCode, c.FailureMessage = OutcomeFailed, "assertion", msg
		return c
	}
	switch report.overall {
	case ComponentDown:
		c.Outcome, c.FailureCode, c.FailureMessage = OutcomeFailed, "health", report.summary("The health check says it's down")
		return c
	case ComponentWarn:
		c.Outcome, c.FailureCode, c.FailureMessage = OutcomeDegraded, "health", report.summary("The health check warns")
		return c
	}
	if report.anyDown() {
		c.Outcome, c.FailureCode, c.FailureMessage = OutcomeDegraded, "health", report.summary("A component is down")
		return c
	}
	if days := mon.CertificateWarningDays; days > 0 && !c.CertificateExpiresAt.IsZero() {
		if left := c.CertificateExpiresAt.Sub(now); left < time.Duration(days)*24*time.Hour {
			c.Outcome, c.FailureCode = OutcomeDegraded, "certificate"
			c.FailureMessage = fmt.Sprintf("The server's certificate expires in %d days, on %s.", int(left.Hours()/24), c.CertificateExpiresAt.UTC().Format("2 January 2006"))
		}
	}
	return c
}

// firstFailure returns why the first assertion that doesn't hold fails, or
// "". Unless a status is asserted, the status must be 2xx.
func firstFailure(assertions []Assertion, r *runner.Response, took time.Duration) string {
	statusAsserted := false
	for _, a := range assertions {
		statusAsserted = statusAsserted || a.Kind == AssertStatus
	}
	if !statusAsserted && (r.Status < 200 || r.Status > 299) {
		return fmt.Sprintf("The response's status is %d, not 2xx.", r.Status)
	}
	for _, a := range assertions {
		if msg := checkOne(a, r, took); msg != "" {
			return msg
		}
	}
	return ""
}

func checkOne(a Assertion, r *runner.Response, took time.Duration) string {
	switch a.Kind {
	case AssertStatus:
		return compare("The response's status", strconv.Itoa(r.Status), true, a)
	case AssertResponseTime:
		return compare("The response time in milliseconds", strconv.FormatInt(took.Milliseconds(), 10), true, a)
	case AssertHeader:
		for _, h := range r.Headers {
			if strings.EqualFold(h.Key, a.Target) {
				return compare("The header "+a.Target, h.Value, true, a)
			}
		}
		return compare("The header "+a.Target, "", false, a)
	case AssertJSONPath:
		value, found, msg := atPath(r, a.Target)
		if msg != "" {
			return msg
		}
		return compare("The value at "+a.Target, value, found, a)
	}
	return ""
}

// atPath reads the first value a JSONPath selects from a response's body.
func atPath(r *runner.Response, target string) (value string, found bool, problem string) {
	if len(r.Body) > maxAssertedBody {
		return "", false, fmt.Sprintf("The body is larger than %d MiB, so %s wasn't read.", maxAssertedBody>>20, target)
	}
	// Compact JSON is YAML, which is what the JSONPath library queries:
	// whitespace is the one place JSON and YAML differ here.
	var compact bytes.Buffer
	if err := json.Compact(&compact, r.Body); err != nil {
		return "", false, fmt.Sprintf("The body isn't JSON, so %s can't be read.", target)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(compact.Bytes(), &doc); err != nil {
		return "", false, fmt.Sprintf("The body couldn't be read, so %s wasn't: %v.", target, err)
	}
	path, err := jsonpath.NewPath(target)
	if err != nil {
		return "", false, fmt.Sprintf("%s isn't a JSONPath.", target)
	}
	root := &doc
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		root = doc.Content[0]
	}
	nodes := path.Query(root)
	if len(nodes) == 0 {
		return "", false, ""
	}
	n := nodes[0]
	if n.Kind == yaml.ScalarNode {
		return n.Value, true, ""
	}
	raw, err := yaml.Marshal(n)
	if err != nil {
		return "", true, ""
	}
	return strings.TrimSpace(string(raw)), true, ""
}

// compare says why a value fails an assertion, or "" when it holds.
func compare(what, got string, found bool, a Assertion) string {
	shown := got
	if len(shown) > 200 {
		shown = shown[:200] + "…"
	}
	switch a.Op {
	case OpExists:
		if !found {
			return what + " is missing."
		}
		return ""
	case OpNotExists:
		if found {
			return what + " should be missing, but is " + strconv.Quote(shown) + "."
		}
		return ""
	}
	if !found {
		return what + " is missing."
	}
	switch a.Op {
	case OpEquals:
		if !same(got, a.Value) {
			return fmt.Sprintf("%s is %q, not %q.", what, shown, a.Value)
		}
	case OpNotEquals:
		if same(got, a.Value) {
			return fmt.Sprintf("%s should not be %q.", what, a.Value)
		}
	case OpContains:
		if !strings.Contains(got, a.Value) {
			return fmt.Sprintf("%s is %q, which doesn't contain %q.", what, shown, a.Value)
		}
	case OpLessThan, OpGreaterThan:
		g, gErr := strconv.ParseFloat(strings.TrimSpace(got), 64)
		v, vErr := strconv.ParseFloat(strings.TrimSpace(a.Value), 64)
		if gErr != nil || vErr != nil {
			return fmt.Sprintf("%s is %q, which isn't a number.", what, shown)
		}
		if a.Op == OpLessThan && g >= v {
			return fmt.Sprintf("%s is %s, not less than %s.", what, shown, a.Value)
		}
		if a.Op == OpGreaterThan && g <= v {
			return fmt.Sprintf("%s is %s, not greater than %s.", what, shown, a.Value)
		}
	}
	return ""
}

// same compares as numbers when both are, so 200 equals 200.0.
func same(a, b string) bool {
	if a == b {
		return true
	}
	x, xErr := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, yErr := strconv.ParseFloat(strings.TrimSpace(b), 64)
	return xErr == nil && yErr == nil && x == y
}

// transition is what a check's outcome does to a monitor. A failure only
// starts a confirmation until enough come in a row; one success ends it.
func transition(state string, failures, threshold int, outcome string) (next string, failuresNext int, confirm bool) {
	switch outcome {
	case OutcomePassed:
		return StateUp, 0, false
	case OutcomeDegraded:
		return StateDegraded, 0, false
	case OutcomeFailed:
		failures++
		if failures >= threshold {
			return StateDown, failures, false
		}
		return state, failures, true
	}
	return state, failures, false
}
