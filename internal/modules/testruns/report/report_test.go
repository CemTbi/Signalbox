// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/testruns/report"
)

func sample() report.Run {
	steps := []report.Step{
		{Seq: 1, Name: "Log in", Method: "POST", URL: "https://api.test/login", Duration: 120 * time.Millisecond,
			Response: &report.Response{Status: 200, StatusText: "OK"},
			Scripts:  &client.ScriptOutcome{Tests: []client.ScriptTest{{Name: "status is 200", Passed: true}, {Name: "later", Skipped: true}}}},
		{Seq: 2, Name: "Create <order>", Path: "Orders", Method: "POST", URL: "https://api.test/orders", Failed: true, Duration: 80 * time.Millisecond,
			Response: &report.Response{Status: 500, StatusText: "Internal Server Error", Body: `{"error":"<script>alert(1)</script>"}`},
			Scripts: &client.ScriptOutcome{
				Tests:  []client.ScriptTest{{Name: "status is 201", Error: "expected 500 to equal 201"}},
				Errors: []client.ScriptError{{From: "Orders", Event: "test", Name: "TypeError", Message: "x is undefined"}},
				Logs:   []client.ScriptLog{{Level: "log", Message: "token •••"}},
			}},
		{Seq: 3, Iteration: 1, Name: "Down", Method: "GET", URL: "https://down.test/", Failed: true,
			Failure: &runner.Failure{Code: "connect", Message: "Couldn't connect to down.test."}},
		{Seq: 5, Iteration: 1, Name: "Script failed", Method: "GET", URL: "https://api.test/s", Failed: true,
			Failure: &runner.Failure{Code: "script", Message: "The pre-request script of Shop API failed, so the request wasn't sent."},
			Scripts: &client.ScriptOutcome{Errors: []client.ScriptError{{From: "Shop API", Event: "prerequest", Name: "ReferenceError", Message: "x is not defined"}}}},
		{Seq: 4, Iteration: 1, Name: "Skipped", Method: "GET", URL: "https://api.test/skip",
			Failure: &runner.Failure{Code: "skipped", Message: "A pre-request script skipped the request, so it wasn't sent."}},
	}
	r := report.Run{ID: "run_1", Name: "Shop API", Environment: "staging", Status: report.Failed, Iterations: 2,
		StartedAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), Duration: 2 * time.Second, Steps: steps}
	for _, s := range steps {
		r.Totals.Add(s)
	}
	return r
}

func TestTotals(t *testing.T) {
	got := sample().Totals
	want := report.Totals{Requests: 5, FailedRequests: 3, Tests: 2, FailedTests: 1}
	if got != want {
		t.Errorf("totals = %+v, want %+v", got, want)
	}
}

func TestJUnit(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, report.JUnit, sample()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Skipped  int `xml:"skipped,attr"`
		Suites   []struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Name    string    `xml:"name,attr"`
				Failure *struct{} `xml:"failure"`
				Error   *struct{} `xml:"error"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, buf.String())
	}
	// Log in: 2 cases (1 skipped). Create: a script error and a failed
	// test. Down: an error. Script failed: one error, not two. Skipped: a
	// skipped case.
	if doc.Tests != 7 || doc.Failures != 1 || doc.Errors != 3 || doc.Skipped != 2 {
		t.Errorf("counts = %d tests, %d failures, %d errors, %d skipped", doc.Tests, doc.Failures, doc.Errors, doc.Skipped)
	}
	if n := doc.Suites[1].Name; n != "Orders / Create <order> (iteration 1)" {
		t.Errorf("suite name = %q", n)
	}
	if c := doc.Suites[2].Cases[0]; c.Error == nil || c.Name != "GET https://down.test/" {
		t.Errorf("failed request = %+v", c)
	}
}

func TestHTML(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, report.HTML, sample()); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	for _, want := range []string{"<title>Shop API: failed</title>", "Create &lt;order&gt;", "expected 500 to equal 201", "&lt;script&gt;alert(1)&lt;/script&gt;", "<details open>"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Nothing runs and nothing loads.
	for _, banned := range []string{"<script", "src=", "href="} {
		if strings.Contains(strings.ToLower(page), banned) {
			t.Errorf("page has %q", banned)
		}
	}
}

func TestJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, report.JSON, sample()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Status     string `json:"status"`
		DurationMs int64  `json:"durationMs"`
		Totals     report.Totals
		Steps      []struct {
			Name       string `json:"name"`
			DurationMs int64  `json:"durationMs"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "failed" || doc.DurationMs != 2000 || len(doc.Steps) != 5 || doc.Steps[0].DurationMs != 120 || doc.Totals.Tests != 2 {
		t.Errorf("json = %+v", doc)
	}
	if err := report.Write(&buf, "pdf", sample()); err == nil {
		t.Error("an unknown format was written")
	}
}
