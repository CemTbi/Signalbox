// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/cli"
)

// shopAPI answers /me with the user and key it was sent, and /orders with
// a 500.
func shopAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/orders" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		fmt.Fprintf(w, `{"user":%q,"key":%q}`, r.URL.Query().Get("user"), r.URL.Query().Get("key"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeFile(t *testing.T, dir, name string, v any) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var data []byte
	if s, ok := v.(string); ok {
		data = []byte(s)
	} else {
		var err error
		if data, err = json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func test(script string) []map[string]any {
	return []map[string]any{{"listen": "test", "script": map[string]any{"exec": []string{script}}}}
}

func shopCollection(base string) map[string]any {
	return map[string]any{
		"info":     map[string]string{"name": "Shop API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
		"variable": []map[string]string{{"key": "baseUrl", "value": base}},
		"item": []map[string]any{
			{"name": "Me", "request": map[string]any{"method": "GET", "url": "{{baseUrl}}/me?user={{user}}&key={{apiKey}}"},
				"event": test(`pm.test("is me", () => pm.expect(pm.response.json().user).to.eql(pm.iterationData.get("user"))); console.log("key", pm.environment.get("apiKey"));`)},
			{"name": "Orders", "item": []map[string]any{
				{"name": "List orders", "request": map[string]any{"method": "GET", "url": "{{baseUrl}}/orders"},
					"event": test(`pm.test("ok", () => pm.response.to.have.status(200));`)},
			}},
		},
	}
}

func TestTestRunOffline(t *testing.T) {
	shop := shopAPI(t)
	dir := t.TempDir()
	col := writeFile(t, dir, "shop.postman_collection.json", shopCollection(shop.URL))
	env := writeFile(t, dir, "local.postman_environment.json", map[string]any{
		"name": "Local", "_postman_variable_scope": "environment",
		"values": []map[string]any{{"key": "apiKey", "value": "k-secret-1", "type": "secret", "enabled": true}},
	})
	data := writeFile(t, dir, "users.csv", "user\nada\nbob\n")
	out := filepath.Join(dir, "reports")

	code, stdout, stderr := run(t, nil, "test", "run", col, "-e", env, "-d", data, "-r", "cli,junit,json,html", "-o", out)
	if code != cli.ExitFindings {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"Shop API · Local · 2 iterations",
		"Iteration 2 of 2",
		"✓ GET Me · 200",
		"✗ GET Orders / List orders · 500",
		"✗ ok\n        AssertionError: expected response to have status code 200 but got 500",
		"Failed: 4 requests (2 failed), 4 tests (2 failed)",
		"Wrote " + filepath.Join(out, "shop-api.xml"),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	junit, err := os.ReadFile(filepath.Join(out, "shop-api.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var suites struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(junit, &suites); err != nil || suites.Tests != 4 || suites.Failures != 2 {
		t.Errorf("junit = %+v, %v", suites, err)
	}
	for _, name := range []string{"shop-api.xml", "shop-api.json", "shop-api.html"} {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "k-secret-1") {
			t.Errorf("%s holds the secret", name)
		}
	}

	// A folder alone, with a variable set on the command line, passes.
	code, stdout, _ = run(t, nil, "test", "run", col, "--folder", "Orders", "--env-var", "baseUrl="+shop.URL+"/ok", "-r", "cli")
	if code != cli.ExitOK || !strings.Contains(stdout, "✓ GET Orders / List orders") || strings.Contains(stdout, "GET Me") {
		t.Errorf("folder run: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = run(t, nil, "test", "run", col, "--folder", "Orders", "--bail", "-n", "3")
	if code != cli.ExitFindings || strings.Count(stdout, "List orders") != 1 || !strings.Contains(stdout, "--bail") {
		t.Errorf("bail: exit %d\n%s", code, stdout)
	}
}

func TestTestRunOfflinePasses(t *testing.T) {
	shop := shopAPI(t)
	dir := t.TempDir()
	col := writeFile(t, dir, "c.json", map[string]any{
		"info": map[string]string{"name": "Me", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
		"item": []map[string]any{{"name": "Me", "request": map[string]any{"method": "GET", "url": shop.URL + "/me"}, "event": test(`pm.test("ok", () => pm.response.to.be.ok);`)}},
	})
	code, stdout, stderr := run(t, nil, "test", "run", col)
	if code != cli.ExitOK || !strings.Contains(stdout, "Passed: 1 request, 1 test") {
		t.Errorf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	// Nothing but the reports.
	code, stdout, _ = run(t, nil, "test", "run", col, "-r", "junit", "-o", dir)
	if code != cli.ExitOK || stdout != "" {
		t.Errorf("junit only: exit %d, %q", code, stdout)
	}
}

func TestTestRunRefusesMetadata(t *testing.T) {
	dir := t.TempDir()
	col := writeFile(t, dir, "c.json", map[string]any{
		"info": map[string]string{"name": "Metadata", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
		"item": []map[string]any{{"name": "Credentials", "request": map[string]any{"method": "GET", "url": "http://169.254.169.254/latest/meta-data/"}}},
	})
	code, stdout, _ := run(t, nil, "test", "run", col)
	if code != cli.ExitFindings || !strings.Contains(stdout, "link-local") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
}

func TestTestRunUsageErrors(t *testing.T) {
	dir := t.TempDir()
	col := writeFile(t, dir, "c.json", shopCollection("http://127.0.0.1:1"))
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{}, "give a Postman collection file"},
		{[]string{col, "--collection", "col_1"}, "not both"},
		{[]string{col, "--server", "https://x.test"}, "not a file"},
		{[]string{col, "-r", "pdf"}, "unknown reporter"},
		{[]string{col, "--folder", "Nope"}, `no folder named "Nope"`},
		{[]string{col, "--env-var", "novalue"}, "isn't key=value"},
		{[]string{col, "-n", "1001"}, "--iterations"},
		{[]string{col, "--allow", "nope"}, "isn't an address range"},
		{[]string{filepath.Join(dir, "missing.json")}, "read the collection"},
		{[]string{writeFile(t, dir, "bad.json", "{")}, "bad.json"},
		{[]string{col, "-d", writeFile(t, dir, "empty.csv", "user\n")}, "no rows"},
		{[]string{"--collection", "col_1"}, "give --server"},
		{[]string{"--collection", "col_1", "--server", "https://x.test"}, "personal access token"},
	} {
		code, _, stderr := runEnv(t, map[string]string{"SIGNALBOX_SERVER": "", "SIGNALBOX_TOKEN": ""}, append([]string{"test", "run"}, tt.args...)...)
		if code != cli.ExitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, %q; want %q", tt.args, code, stderr, tt.want)
		}
	}
}

// runEnv runs the CLI with environment variables set, and prints the
// error as main does.
func runEnv(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	code, stdout, stderr, err := runErr(t, args...)
	if err != nil {
		stderr += err.Error()
	}
	return code, stdout, stderr
}

func TestTestRunOnServer(t *testing.T) {
	defer cli.PollFast()()
	var (
		mu      sync.Mutex
		started map[string]any
		polls   int
	)
	steps := []string{
		`{"seq":1,"iteration":0,"name":"Me","method":"GET","url":"https://api.test/me","failed":false,"durationMs":12.5,"response":{"status":200,"size":9},"scripts":{"tests":[{"from":"Me","name":"is me","passed":true}],"logs":[],"errors":[],"changes":[],"skipped":false}}`,
		`{"seq":2,"iteration":0,"name":"List orders","path":"Orders","method":"GET","url":"https://api.test/orders","failed":true,"durationMs":8,"response":{"status":500,"size":2},"scripts":{"tests":[{"from":"Orders","name":"ok","passed":false,"error":"expected 500 to equal 200"}],"logs":[],"errors":[],"changes":[],"skipped":false}}`,
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer sbx_pat_test" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"code":"unauthorized","detail":"Sign in."}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/collections/col_1":
			fmt.Fprint(w, `{"id":"col_1","projectId":"prj_1","name":"Shop API"}`)
		case "GET /api/v1/projects/prj_1/environments":
			fmt.Fprint(w, `{"data":[{"id":"env_1","name":"Development","slug":"development"},{"id":"env_2","name":"Staging","slug":"staging"}]}`)
		case "GET /api/v1/collections/col_1/items":
			fmt.Fprint(w, `{"data":[{"id":"item_9","kind":"folder","name":"Orders"}]}`)
		case "POST /api/v1/collections/col_1/runs":
			_ = json.NewDecoder(r.Body).Decode(&started)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"run_1","projectId":"prj_1","name":"Shop API","environment":"Staging","status":"queued","iterations":1,"totals":{"requests":0,"failedRequests":0,"tests":0,"failedTests":0},"durationMs":0}`)
		case "GET /api/v1/runs/run_1":
			polls++
			status, n := "running", polls-1
			if polls >= 3 {
				status, n = "failed", 2
			}
			fmt.Fprintf(w, `{"id":"run_1","name":"Shop API","status":%q,"iterations":1,"totals":{"requests":%d,"failedRequests":%d,"tests":%d,"failedTests":%d},"durationMs":1500}`,
				status, n, n/2, n, n/2)
		case "GET /api/v1/runs/run_1/steps":
			after, _ := strconv.Atoi(r.URL.Query().Get("after"))
			upTo := min(polls, len(steps))
			var page []string
			for i := after; i < upTo; i++ {
				page = append(page, steps[i])
			}
			fmt.Fprintf(w, `{"data":[%s],"more":false}`, strings.Join(page, ","))
		case "GET /api/v1/runs/run_1/report":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<testsuites name=%q/>", r.URL.Query().Get("format"))
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"code":"not-found","detail":"There is no such thing."}`)
		}
	}))
	t.Cleanup(api.Close)
	dir := t.TempDir()
	data := writeFile(t, dir, "users.json", `[{"user":"ada"}]`)

	code, stdout, stderr := runEnv(t, map[string]string{"SIGNALBOX_SERVER": api.URL, "SIGNALBOX_TOKEN": "sbx_pat_test"},
		"test", "run", "--collection", "col_1", "--environment", "staging", "--folder", "Orders", "-d", data, "--bail", "-r", "cli,junit", "-o", dir)
	if code != cli.ExitFindings {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if started["environmentId"] != "env_2" || started["folderId"] != "item_9" || started["bail"] != true || started["data"].(map[string]any)["format"] != "json" {
		t.Errorf("started with %v", started)
	}
	for _, want := range []string{"Shop API · Staging", "✓ GET Me · 200 · 12 ms", "✗ GET Orders / List orders · 500", "expected 500 to equal 200", "Failed: 2 requests (1 failed)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Count(stdout, "GET Me") != 1 {
		t.Errorf("a step was printed twice:\n%s", stdout)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "shop-api.xml")); err != nil || string(b) != `<testsuites name="junit"/>` {
		t.Errorf("report = %q, %v", b, err)
	}

	// The github reporter annotates and summarizes a server's run too.
	summary := filepath.Join(dir, "summary.md")
	code, stdout, stderr = runEnv(t, map[string]string{"SIGNALBOX_SERVER": api.URL, "SIGNALBOX_TOKEN": "sbx_pat_test", "GITHUB_STEP_SUMMARY": summary},
		"test", "run", "--collection", "col_1", "-r", "github")
	if code != cli.ExitFindings {
		t.Fatalf("github: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if want := "::error title=GET Orders / List orders::ok: expected 500 to equal 200\n"; stdout != want {
		t.Errorf("annotations = %q, want %q", stdout, want)
	}
	b, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"### ❌ Shop API: Failed", "| 2 | 1 | 2 | 1 | 1.5 s |", "- **GET Orders / List orders** · 500", "  - ✗ ok: expected 500 to equal 200"} {
		if !strings.Contains(string(b), w) {
			t.Errorf("summary lacks %q:\n%s", w, b)
		}
	}

	// What the server refuses says why.
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--collection", "col_2"}, "There is no such thing."},
		{[]string{"--collection", "col_1", "--environment", "qa"}, "it has development, staging"},
		{[]string{"--collection", "col_1", "--folder", "Refunds"}, `no folder "Refunds"`},
		{[]string{"--collection", "col_1", "--token", "wrong"}, "the server answered 401: Sign in."},
	} {
		code, _, stderr := runEnv(t, map[string]string{"SIGNALBOX_SERVER": api.URL, "SIGNALBOX_TOKEN": "sbx_pat_test"}, append([]string{"test", "run"}, tt.args...)...)
		if code != cli.ExitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, %q", tt.args, code, stderr)
		}
	}
}

func TestTestRunOnServerCancels(t *testing.T) {
	defer cli.PollFast()()
	canceled := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/collections/col_1":
			fmt.Fprint(w, `{"id":"col_1","projectId":"prj_1"}`)
		case "POST /api/v1/collections/col_1/runs", "GET /api/v1/runs/run_1":
			fmt.Fprint(w, `{"id":"run_1","name":"Shop API","status":"running","iterations":1,"totals":{"requests":0,"failedRequests":0,"tests":0,"failedTests":0}}`)
		case "GET /api/v1/runs/run_1/steps":
			fmt.Fprint(w, `{"data":[],"more":false}`)
		case "POST /api/v1/runs/run_1/cancel":
			close(canceled)
			fmt.Fprint(w, `{"id":"run_1","status":"canceled"}`)
		}
	}))
	t.Cleanup(api.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := cli.NewRootCommand(strings.NewReader(""), &out, &errOut)
	cmd.SetArgs([]string{"test", "run", "--server", api.URL, "--token", "t", "--collection", "col_1"})
	err := cmd.ExecuteContext(ctx)
	if cli.ExitCode(err) != cli.ExitFindings || !strings.Contains(errOut.String(), "Cancelled the run.") {
		t.Errorf("exit %d, %v\n%s", cli.ExitCode(err), err, errOut.String())
	}
	select {
	case <-canceled:
	default:
		t.Error("the run wasn't cancelled on the server")
	}
}

// runErr executes the CLI and returns its exit code, output and error.
func runErr(t *testing.T, args ...string) (int, string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := cli.NewRootCommand(strings.NewReader(""), &out, &errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return cli.ExitCode(err), out.String(), errOut.String(), err
}

func TestTestRunGitHubReporter(t *testing.T) {
	shop := shopAPI(t)
	dir := t.TempDir()
	col := writeFile(t, dir, "shop.json", shopCollection(shop.URL))
	summary := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(summary, []byte("Earlier step\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runEnv(t, map[string]string{"GITHUB_STEP_SUMMARY": summary}, "test", "run", col, "--folder", "Orders", "-r", "github")
	if code != cli.ExitFindings {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	// Annotations, without the cli reporter's lines.
	want := "::error title=GET Orders / List orders::ok: AssertionError: expected response to have status code 200 but got 500\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	b, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, w := range []string{"Earlier step\n### ❌ Orders: Failed", "| 1 | 1 | 1 | 1 |", "- **GET Orders / List orders** · 500", "  - ✗ ok: AssertionError"} {
		if !strings.Contains(got, w) {
			t.Errorf("summary lacks %q:\n%s", w, got)
		}
	}

	// Names can't inject Markdown, HTML or workflow commands.
	hostile := writeFile(t, dir, "hostile.json", map[string]any{
		"info": map[string]string{"name": "<b>x</b> | y", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
		"item": []map[string]any{{"name": "a::b,\nc", "request": map[string]any{"method": "GET", "url": shop.URL + "/orders"},
			"event": test(`pm.test("[link](https://evil.test)", () => pm.response.to.be.ok);`)}},
	})
	_, stdout, _ = runEnv(t, map[string]string{"GITHUB_STEP_SUMMARY": summary}, "test", "run", hostile, "-r", "github")
	if !strings.HasPrefix(stdout, "::error title=GET a%3A%3Ab%2C%0Ac::") {
		t.Errorf("annotation = %q", stdout)
	}
	b, _ = os.ReadFile(summary)
	if strings.Contains(string(b), "<b>") || strings.Contains(string(b), "[link](") || !strings.Contains(string(b), `\[link\]`) {
		t.Errorf("summary isn't escaped:\n%s", b)
	}
}
