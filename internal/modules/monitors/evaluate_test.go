// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func TestTransition(t *testing.T) {
	cases := []struct {
		name            string
		state           string
		failures, limit int
		outcome         string
		wantState       string
		wantFailures    int
		wantConfirm     bool
	}{
		{"first success", StatePending, 0, 2, OutcomePassed, StateUp, 0, false},
		{"a failure only starts a confirmation", StateUp, 0, 2, OutcomeFailed, StateUp, 1, true},
		{"enough in a row make it down", StateUp, 1, 2, OutcomeFailed, StateDown, 2, false},
		{"one is enough if it's set to", StateUp, 0, 1, OutcomeFailed, StateDown, 1, false},
		{"a pending monitor stays pending while it confirms", StatePending, 0, 2, OutcomeFailed, StatePending, 1, true},
		{"one success brings it back", StateDown, 5, 2, OutcomePassed, StateUp, 0, false},
		{"a success ends a confirmation", StateUp, 1, 3, OutcomePassed, StateUp, 0, false},
		{"degraded needs no confirmation", StateUp, 0, 2, OutcomeDegraded, StateDegraded, 0, false},
		{"down stays down, without confirming again", StateDown, 2, 2, OutcomeFailed, StateDown, 3, false},
		{"skipped changes nothing", StateDown, 2, 2, OutcomeSkipped, StateDown, 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, failures, confirm := transition(c.state, c.failures, c.limit, c.outcome)
			if state != c.wantState || failures != c.wantFailures || confirm != c.wantConfirm {
				t.Errorf("got %s, %d, %v; want %s, %d, %v", state, failures, confirm, c.wantState, c.wantFailures, c.wantConfirm)
			}
		})
	}
}

func response(status int, body string, headers ...runner.Header) *runner.Response {
	return &runner.Response{Status: status, Body: []byte(body), Headers: headers}
}

func TestFirstFailure(t *testing.T) {
	jsonBody := `{"status": "ok", "count": 3,	"items": [{"id": "a"}, {"id": "b"}], "nothing": null}`
	cases := []struct {
		name       string
		response   *runner.Response
		assertions []Assertion
		want       string // part of the message; "" means it holds
	}{
		{"a 2xx holds by default", response(204, ""), nil, ""},
		{"anything else doesn't", response(503, ""), nil, "503, not 2xx"},
		{"a redirect isn't 2xx", response(304, ""), nil, "304"},
		{"asserting a status replaces the default", response(503, ""), []Assertion{{Kind: AssertStatus, Op: OpEquals, Value: "503"}}, ""},
		{"a status that isn't", response(200, ""), []Assertion{{Kind: AssertStatus, Op: OpEquals, Value: "201"}}, `"200", not "201"`},
		{"status below", response(200, ""), []Assertion{{Kind: AssertStatus, Op: OpLessThan, Value: "300"}}, ""},
		{"other assertions keep the default", response(500, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.status", Op: OpEquals, Value: "ok"}}, "500, not 2xx"},
		{"a header is found whatever its case", response(200, "", runner.Header{Key: "Content-Type", Value: "text/plain"}), []Assertion{{Kind: AssertHeader, Target: "content-type", Op: OpContains, Value: "text"}}, ""},
		{"a missing header", response(200, ""), []Assertion{{Kind: AssertHeader, Target: "X-Id", Op: OpExists}}, "X-Id is missing"},
		{"a header that shouldn't be there", response(200, "", runner.Header{Key: "Server", Value: "x"}), []Assertion{{Kind: AssertHeader, Target: "Server", Op: OpNotExists}}, "should be missing"},
		{"a value at a path", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.status", Op: OpEquals, Value: "ok"}}, ""},
		{"a wrong value at a path", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.status", Op: OpEquals, Value: "bad"}}, `"ok", not "bad"`},
		{"a number at a path", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.count", Op: OpGreaterThan, Value: "2"}}, ""},
		{"numbers compare as numbers", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.count", Op: OpEquals, Value: "3.0"}}, ""},
		{"not a number", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.status", Op: OpLessThan, Value: "2"}}, "isn't a number"},
		{"a filter", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.items[?(@.id == 'b')].id", Op: OpEquals, Value: "b"}}, ""},
		{"a path that matches nothing", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.missing", Op: OpExists}}, "$.missing is missing"},
		{"a path that should match nothing", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.missing", Op: OpNotExists}}, ""},
		{"a null exists", response(200, jsonBody), []Assertion{{Kind: AssertJSONPath, Target: "$.nothing", Op: OpExists}}, ""},
		{"a body that isn't JSON", response(200, "<html>"), []Assertion{{Kind: AssertJSONPath, Target: "$.a", Op: OpExists}}, "isn't JSON"},
		{"a body too large to parse", response(200, `"`+strings.Repeat("a", maxAssertedBody)+`"`), []Assertion{{Kind: AssertJSONPath, Target: "$.a", Op: OpExists}}, "larger than"},
		{"the first failure is told", response(200, ""), []Assertion{{Kind: AssertStatus, Op: OpEquals, Value: "200"}, {Kind: AssertHeader, Target: "A", Op: OpExists}, {Kind: AssertHeader, Target: "B", Op: OpExists}}, "header A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstFailure(c.assertions, c.response, 40*time.Millisecond)
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	over := []Assertion{{Kind: AssertResponseTime, Op: OpLessThan, Value: "10"}}
	if got := firstFailure(over, response(200, ""), 40*time.Millisecond); !strings.Contains(got, "40, not less than 10") {
		t.Errorf("too slow: %q", got)
	}
}

func TestReadHealth(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		overall    string
		components map[string]string
	}{
		{
			"health+json", `{"status":"pass","checks":{"postgres:responseTime":[{"componentId":"postgres","status":"pass"}],"memory:used":[{"status":"warn","output":"high"}]}}`,
			ComponentOK, map[string]string{"postgres:responseTime": ComponentOK, "memory:used": ComponentWarn},
		},
		{"health+json warns", `{"status":"warn"}`, ComponentWarn, nil},
		{"health+json fails", `{"status":"fail","checks":{"db":[{"status":"fail"}]}}`, ComponentDown, map[string]string{"db": ComponentDown}},
		{
			"Spring Boot", `{"status":"UP","components":{"db":{"status":"UP","details":{"database":"PostgreSQL"}},"mail":{"status":"DOWN"},"ping":{"status":"UNKNOWN"}}}`,
			ComponentOK, map[string]string{"db": ComponentOK, "mail": ComponentDown, "ping": ComponentWarn},
		},
		{"Spring Boot before 2.2", `{"status":"OUT_OF_SERVICE","details":{"db":{"status":"UP"}}}`, ComponentDown, map[string]string{"db": ComponentOK}},
		{"Kubernetes passing", "[+]ping ok\n[+]etcd ok\nreadyz check passed\n", ComponentOK, map[string]string{"ping": ComponentOK, "etcd": ComponentOK}},
		{"Kubernetes failing", "[+]ping ok\n[-]etcd failed: reason withheld\nreadyz check failed\n", ComponentDown, map[string]string{"ping": ComponentOK, "etcd": ComponentDown}},
		{"other JSON says nothing", `{"hello":"world"}`, "", nil},
		{"a status it doesn't know says nothing", `{"status":"great"}`, "", nil},
		{"text says nothing", "OK", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readHealth(response(200, c.body))
			if got.overall != c.overall {
				t.Errorf("overall = %q, want %q", got.overall, c.overall)
			}
			if len(got.components) != len(c.components) {
				t.Fatalf("components = %+v", got.components)
			}
			for _, part := range got.components {
				if c.components[part.Name] != part.Status {
					t.Errorf("%s = %s, want %s", part.Name, part.Status, c.components[part.Name])
				}
			}
		})
	}
}

func TestManyComponentsNeverHideOneThatsDown(t *testing.T) {
	var lines, checks []string
	for i := range maxComponents + 10 {
		lines = append(lines, fmt.Sprintf("[+]check-%d ok", i))
		checks = append(checks, fmt.Sprintf(`"check-%d":[{"status":"pass"}]`, i))
	}
	kubernetes := readHealth(response(503, strings.Join(lines, "\n")+"\n[-]zz-etcd failed: reason withheld\nreadyz check failed\n"))
	if kubernetes.overall != ComponentDown || !kubernetes.anyDown() || len(kubernetes.components) != maxComponents || kubernetes.components[0].Name != "zz-etcd" {
		t.Errorf("Kubernetes = %s, %d components, first %+v", kubernetes.overall, len(kubernetes.components), kubernetes.components[0])
	}
	// The whole is up, so the one that's down is what makes it degraded.
	body := `{"status":"pass","checks":{` + strings.Join(checks, ",") + `,"zz-db":[{"status":"fail"}]}}`
	got := evaluate(&Monitor{Kind: KindHealth}, client.Entry{Result: runner.Result{Response: response(200, body)}}, time.Now())
	if got.Outcome != OutcomeDegraded || len(got.Components) != maxComponents || got.Components[0].Name != "zz-db" {
		t.Errorf("health+json = %s, %d components, first %+v", got.Outcome, len(got.Components), got.Components[0])
	}
}

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	mon := func(kind string, days int) *Monitor {
		return &Monitor{ID: uuid.New(), EnvironmentID: uuid.New(), Kind: kind, CertificateWarningDays: days}
	}
	entry := func(r *runner.Response) client.Entry {
		return client.Entry{Result: runner.Result{Response: r, Timings: runner.Timings{Total: 50 * time.Millisecond}}, TraceID: "abc"}
	}
	cases := []struct {
		name    string
		mon     *Monitor
		entry   client.Entry
		outcome string
		code    string
	}{
		{"no response", mon(KindHTTP, 14), client.Entry{Result: runner.Result{Failure: &runner.Failure{Code: "dns", Message: "no such host"}}}, OutcomeFailed, "dns"},
		{"a bad status", mon(KindHTTP, 14), entry(response(500, "")), OutcomeFailed, "assertion"},
		{"fine", mon(KindHTTP, 14), entry(response(200, "")), OutcomePassed, ""},
		// Only a health monitor reads the body.
		{"an http monitor ignores health", mon(KindHTTP, 14), entry(response(200, `{"status":"fail"}`)), OutcomePassed, ""},
		{"down, with a 200", mon(KindHealth, 14), entry(response(200, `{"status":"fail"}`)), OutcomeFailed, "health"},
		{"warns", mon(KindHealth, 14), entry(response(200, `{"status":"warn"}`)), OutcomeDegraded, "health"},
		{"a component down while the whole is up", mon(KindHealth, 14), entry(response(200, `{"status":"pass","checks":{"db":[{"status":"fail"}]}}`)), OutcomeDegraded, "health"},
		{"a component warning while the whole is up", mon(KindHealth, 14), entry(response(200, `{"status":"pass","checks":{"db":[{"status":"warn"}]}}`)), OutcomePassed, ""},
		{"health it can't read rests on the status", mon(KindHealth, 14), entry(response(200, "OK")), OutcomePassed, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evaluate(c.mon, c.entry, now)
			if got.Outcome != c.outcome || got.FailureCode != c.code {
				t.Errorf("got %s, %q (%s); want %s, %q", got.Outcome, got.FailureCode, got.FailureMessage, c.outcome, c.code)
			}
		})
	}

	// A check that fails still keeps the components it read.
	failing := evaluate(mon(KindHealth, 14), entry(response(503, `{"status":"fail","checks":{"db":[{"status":"fail"}]}}`)), now)
	if failing.Outcome != OutcomeFailed || failing.FailureCode != "assertion" || len(failing.Components) != 1 || failing.Status != 503 || failing.TraceID != "abc" {
		t.Errorf("failing = %+v", failing)
	}

	expiring := response(200, "")
	expiring.CertificateExpiresAt = now.Add(5 * 24 * time.Hour)
	got := evaluate(mon(KindHTTP, 14), entry(expiring), now)
	if got.Outcome != OutcomeDegraded || got.FailureCode != "certificate" || !strings.Contains(got.FailureMessage, "5 days") || !got.CertificateExpiresAt.Equal(expiring.CertificateExpiresAt) {
		t.Errorf("expiring soon = %+v", got)
	}
	if got := evaluate(mon(KindHTTP, 3), entry(expiring), now); got.Outcome != OutcomePassed {
		t.Errorf("further off than the warning = %+v", got)
	}
	if got := evaluate(mon(KindHTTP, 0), entry(expiring), now); got.Outcome != OutcomePassed {
		t.Errorf("with the warning off = %+v", got)
	}
}

func TestCheckAssertion(t *testing.T) {
	cases := []struct {
		name    string
		a       Assertion
		pointer string // "" means valid
	}{
		{"a status", Assertion{Kind: AssertStatus, Op: OpEquals, Value: "200"}, ""},
		{"a status that isn't a number", Assertion{Kind: AssertStatus, Op: OpEquals, Value: "ok"}, "/a/value"},
		{"an unknown kind", Assertion{Kind: "body", Op: OpEquals}, "/a/kind"},
		{"an op the kind doesn't take", Assertion{Kind: AssertResponseTime, Op: OpEquals, Value: "1"}, "/a/op"},
		{"a header without a name", Assertion{Kind: AssertHeader, Op: OpExists}, "/a/target"},
		{"a JSONPath", Assertion{Kind: AssertJSONPath, Target: "$.a[0]", Op: OpExists}, ""},
		{"not a JSONPath", Assertion{Kind: AssertJSONPath, Target: "a b", Op: OpExists}, "/a/target"},
		{"no JSONPath", Assertion{Kind: AssertJSONPath, Op: OpExists}, "/a/target"},
		{"comparing with something else than a number", Assertion{Kind: AssertJSONPath, Target: "$.a", Op: OpLessThan, Value: "x"}, "/a/value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var errs validate.Errors
			checkAssertion(&errs, "/a", &c.a)
			if c.pointer == "" && len(errs) > 0 || c.pointer != "" && (len(errs) == 0 || errs[0].Pointer != c.pointer) {
				t.Errorf("errors = %+v, want %q", errs, c.pointer)
			}
		})
	}
}

func TestFirstCheckIsAtTheMonitorsPhase(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 30, 0, time.UTC)
	seen := map[time.Duration]bool{}
	for range 50 {
		monitor := uuid.New()
		at := firstCheck(monitor, time.Minute, now)
		if !at.After(now) || at.Sub(now) > time.Minute {
			t.Errorf("first check at %s, from %s", at, now)
		}
		// The same monitor keeps the same place in the interval.
		if later := firstCheck(monitor, time.Minute, now.Add(17*time.Minute)); later.Sub(at)%time.Minute != 0 {
			t.Errorf("moved from %s to %s", at, later)
		}
		seen[at.Sub(now.Truncate(time.Minute))] = true
	}
	// Monitors are spread, not lined up.
	if len(seen) < 40 {
		t.Errorf("only %d places among 50 monitors", len(seen))
	}
}
