// SPDX-License-Identifier: Apache-2.0

package scripts_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/scripts"
)

var orderResponse = &scripts.Response{
	Code:         201,
	Status:       "Created",
	Header:       []scripts.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Location", Value: "/orders/42"}},
	Body:         `{"id": 42, "items": [{"sku": "A-1", "qty": 2}], "token": "abc"}`,
	ResponseTime: 87,
}

func run(t *testing.T, s *scripts.Session, in scripts.Input) *scripts.Result {
	t.Helper()
	if in.Request.URL == "" {
		in.Request = scripts.Request{URL: "https://shop.example.com/orders", Method: "POST"}
	}
	if in.Info.RequestName == "" {
		in.Info.RequestName = "Create order"
	}
	res, err := s.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func session(t *testing.T, opts scripts.Options) *scripts.Session {
	t.Helper()
	s, err := scripts.NewSession(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func passed(t *testing.T, res *scripts.Result, want ...string) {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("script error: %v", res.Error)
	}
	got := map[string]bool{}
	for _, tr := range res.Tests {
		got[tr.Name] = tr.Passed
		if !tr.Passed {
			t.Errorf("test %q failed: %v", tr.Name, tr.Error)
		}
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("test %q didn't run; ran %v", name, res.Tests)
		}
	}
}

func TestModernTestScript(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "test", Response: orderResponse, Source: `
pm.test("Status code is 201", function () {
    pm.response.to.have.status(201);
});
pm.test("Has a Location header", () => {
    pm.response.to.have.header("Location", "/orders/42");
    pm.expect(pm.response.headers.get("content-type")).to.include("json");
});
pm.test("Body is an order", () => {
    const order = pm.response.json();
    pm.expect(order.id).to.be.a("number").and.equal(42);
    pm.expect(order.items).to.have.lengthOf(1);
    pm.expect(order).to.have.nested.property("items[0].sku", "A-1");
    pm.response.to.be.json;
    pm.response.to.have.jsonBody("token");
});
pm.test("Fast enough", () => pm.expect(pm.response.responseTime).to.be.below(200));
pm.environment.set("orderId", pm.response.json().id);
console.log("created order", pm.response.json().id);
`})
	passed(t, res, "Status code is 201", "Has a Location header", "Body is an order", "Fast enough")
	if res.Variables.Environment["orderId"] != float64(42) {
		t.Errorf("environment = %v", res.Variables.Environment)
	}
	if len(res.Logs) != 1 || res.Logs[0].Message != "created order 42" {
		t.Errorf("logs = %v", res.Logs)
	}
}

func TestLegacyTestScript(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "test", Response: orderResponse, Source: `
var data = JSON.parse(responseBody);
tests["Status code is 201"] = responseCode.code === 201;
tests["Has an id"] = data.id === 42;
tests["Wrong on purpose"] = responseTime > 1000;
postman.setEnvironmentVariable("token", data.token);
tests["Location"] = postman.getResponseHeader("location") === "/orders/42";
`})
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	want := map[string]bool{"Status code is 201": true, "Has an id": true, "Wrong on purpose": false, "Location": true}
	for _, tr := range res.Tests {
		if w, ok := want[tr.Name]; !ok || w != tr.Passed {
			t.Errorf("%q passed = %v", tr.Name, tr.Passed)
		}
	}
	if res.Variables.Environment["token"] != "abc" {
		t.Errorf("environment = %v", res.Variables.Environment)
	}
}

func TestFailingAssertionsAreReported(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "test", Response: orderResponse, Source: `
pm.test("Status code is 200", () => pm.response.to.have.status(200));
pm.test("Still runs", () => {});
`})
	if len(res.Tests) != 2 || res.Tests[0].Passed || !res.Tests[1].Passed {
		t.Fatalf("tests = %+v", res.Tests)
	}
	if e := res.Tests[0].Error; e == nil || e.Name != "AssertionError" || !strings.Contains(e.Message, "201") {
		t.Errorf("error = %+v", e)
	}
}

func TestPreRequestScript(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "prerequest",
		Request: scripts.Request{URL: "https://{{host}}/orders?page=1", Method: "POST",
			Body: &scripts.Body{Mode: "raw", Raw: `{"sku":"A-1"}`}},
		Variables: scripts.Variables{Environment: map[string]any{"host": "shop.example.com", "secret": "s3cret"}},
		Source: `
const ts = moment.utc("2026-10-17T12:00:00Z").unix();
const sig = CryptoJS.HmacSHA256(pm.request.body.raw + ts, pm.environment.get("secret")).toString(CryptoJS.enc.Hex);
pm.request.headers.add({ key: "X-Signature", value: sig });
pm.request.url.query.add({ key: "ts", value: String(ts) });
pm.variables.set("requestId", require("uuid").v4());
`})
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	if res.Request == nil {
		t.Fatal("no request after the pre-request script")
	}
	sig := ""
	for _, h := range res.Request.Header {
		if h.Key == "X-Signature" {
			sig = h.Value
		}
	}
	// HMAC-SHA256 of `{"sku":"A-1"}1792238400` with the key s3cret.
	if len(sig) != 64 {
		t.Errorf("signature header = %q", sig)
	}
	if id, _ := res.Variables.Local["requestId"].(string); len(id) != 36 {
		t.Errorf("local variables = %v", res.Variables.Local)
	}
}

func TestVariablePrecedence(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "test",
		Variables: scripts.Variables{
			Globals:     map[string]any{"a": "global", "b": "global", "c": "global"},
			Collection:  map[string]any{"b": "collection", "c": "collection"},
			Environment: map[string]any{"c": "environment"},
		}, Source: `
pm.test("narrowest scope wins", () => {
    pm.expect(pm.variables.get("a")).to.equal("global");
    pm.expect(pm.variables.get("b")).to.equal("collection");
    pm.expect(pm.variables.get("c")).to.equal("environment");
    pm.expect(pm.variables.replaceIn("{{a}}-{{c}}-{{missing}}")).to.equal("global-environment-{{missing}}");
});
pm.collectionVariables.unset("b");
`})
	passed(t, res, "narrowest scope wins")
	if _, ok := res.Variables.Collection["b"]; ok {
		t.Error("collection variable b wasn't unset")
	}
}

func TestSendRequest(t *testing.T) {
	var sent []scripts.Request
	s := session(t, scripts.Options{Send: func(_ context.Context, req scripts.Request) (*scripts.Response, error) {
		sent = append(sent, req)
		return &scripts.Response{Code: 200, Body: `{"access_token":"t0k"}`}, nil
	}})
	res := run(t, s, scripts.Input{Event: "prerequest", Source: `
pm.sendRequest({
    url: "https://auth.example.com/token",
    method: "POST",
    header: { "Content-Type": "application/json" },
    body: { mode: "raw", raw: JSON.stringify({ client: "shop" }) }
}, function (err, res) {
    pm.test("token request worked", () => pm.expect(err).to.equal(null));
    pm.environment.set("token", res.json().access_token);
});
`})
	passed(t, res, "token request worked")
	if len(sent) != 1 || sent[0].URL != "https://auth.example.com/token" || sent[0].Method != http.MethodPost {
		t.Errorf("sent = %+v", sent)
	}
	if res.Variables.Environment["token"] != "t0k" {
		t.Errorf("environment = %v", res.Variables.Environment)
	}
}

func TestNextRequest(t *testing.T) {
	s := session(t, scripts.Options{})
	for _, tt := range []struct {
		source string
		want   *string
	}{
		{`pm.test("none", () => {});`, nil},
		{`pm.execution.setNextRequest("Pay");`, ptr("Pay")},
		{`postman.setNextRequest(null);`, ptr("")},
	} {
		res := run(t, s, scripts.Input{Event: "test", Source: tt.source, Response: orderResponse})
		if (res.NextRequest == nil) != (tt.want == nil) || res.NextRequest != nil && *res.NextRequest != *tt.want {
			t.Errorf("%s: next request = %v", tt.source, res.NextRequest)
		}
	}
}

func ptr(s string) *string { return &s }

func TestTimersAndDone(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "test", Source: `
pm.test("later", function (done) {
    setTimeout(() => { pm.environment.set("waited", true); done(); }, 20);
});
`})
	passed(t, res, "later")
	if res.Variables.Environment["waited"] != true {
		t.Errorf("environment = %v", res.Variables.Environment)
	}
}

func TestErrorsInScripts(t *testing.T) {
	s := session(t, scripts.Options{})
	res := run(t, s, scripts.Input{Event: "test", Source: "pm.test('fine', () => {});\nundefinedFunction();"})
	if res.Error == nil || res.Error.Name != "ReferenceError" || len(res.Tests) != 1 {
		t.Errorf("runtime error: %+v, tests %v", res.Error, res.Tests)
	}
	res = run(t, s, scripts.Input{Event: "test", Source: "pm.test('broken', () => {"})
	if res.Error == nil || res.Error.Name != "SyntaxError" {
		t.Errorf("syntax error: %+v", res.Error)
	}
	res = run(t, s, scripts.Input{Event: "test", Source: `require("fs")`})
	if res.Error == nil || !strings.Contains(res.Error.Message, `"fs" isn't available`) {
		t.Errorf("unknown module: %+v", res.Error)
	}
}

func TestTimeout(t *testing.T) {
	s := session(t, scripts.Options{Timeout: 200 * time.Millisecond})
	start := time.Now()
	_, err := s.Run(context.Background(), scripts.Input{Event: "test", Source: "while (true) {}", Info: scripts.Info{RequestName: "loop"}})
	if !errors.Is(err, scripts.ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stopped after %v", d)
	}
	// The session still works afterwards.
	res := run(t, s, scripts.Input{Event: "test", Source: "pm.test('after', () => {})"})
	passed(t, res, "after")
}

func TestSharedHelpersWithinASession(t *testing.T) {
	// A common pattern: one script stores functions in a global variable,
	// later scripts eval it.
	s := session(t, scripts.Options{})
	vars := scripts.Variables{}
	res := run(t, s, scripts.Input{Event: "prerequest", Variables: vars, Source: `
pm.globals.set("helpers", "(function () { return { double: function (n) { return n * 2; } }; })()");
`})
	vars = res.Variables
	res = run(t, s, scripts.Input{Event: "test", Variables: vars, Source: `
var helpers = eval(pm.globals.get("helpers"));
pm.test("helper works", () => pm.expect(helpers.double(21)).to.equal(42));
`})
	passed(t, res, "helper works")
}

func TestSessionsAreIsolated(t *testing.T) {
	a := session(t, scripts.Options{})
	run(t, a, scripts.Input{Event: "test", Source: `Array.prototype.leak = 1; globalThis.leaked = true;`})
	b := session(t, scripts.Options{})
	res := run(t, b, scripts.Input{Event: "test", Source: `
pm.test("clean", () => {
    pm.expect([].leak).to.equal(undefined);
    pm.expect(typeof leaked).to.equal("undefined");
});`})
	passed(t, res, "clean")
}

func TestPoolHandsOutWarmSessions(t *testing.T) {
	p := scripts.NewPool(1, scripts.Options{})
	defer p.Close()
	time.Sleep(500 * time.Millisecond) // let it warm one
	s, err := p.Session()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := run(t, s, scripts.Input{Event: "test", Response: orderResponse, Source: `pm.test("ok", () => pm.response.to.have.status(201))`})
	passed(t, res, "ok")
	if d := time.Since(start); d > 30*time.Millisecond {
		t.Logf("a warm session took %v for its first script", d)
	}
}

func TestOlderSandboxHelpers(t *testing.T) {
	res := run(t, session(t, scripts.Options{}), scripts.Input{Event: "test",
		Response: &scripts.Response{Code: 200, Body: `<order id="42"><item sku="A-1">2</item></order>`,
			Header: []scripts.Header{{Key: "Content-Type", Value: "application/xml"}}},
		Source: `
var order = xml2Json(responseBody).order;
tests["xml2Json"] = order.$.id === "42" && order.item._ === "2";
tests["responseBody.has"] = responseBody.has("A-1") && !responseBody.has("B-2");
tests["atob and btoa"] = atob(btoa("hi")) === "hi" && require("btoa")("hi") === "aGk=";
tests["url"] = require("url").parse("https://shop.example.com/orders?page=2", true).query.page === "2";
`})
	passed(t, res, "xml2Json", "responseBody.has", "atob and btoa", "url")
}

func TestSendRequestBodies(t *testing.T) {
	var sent []scripts.Request
	s := session(t, scripts.Options{Send: func(_ context.Context, req scripts.Request) (*scripts.Response, error) {
		sent = append(sent, req)
		return &scripts.Response{Code: 200, Body: "{}"}, nil
	}})
	res := run(t, s, scripts.Input{Event: "test", Source: `
pm.sendRequest({ url: "https://api.example.com/a", method: "POST", body: { mode: "raw", raw: { id: 1 } } }, () => {});
pm.sendRequest({ url: "https://api.example.com/b", method: "POST",
    body: { mode: "urlencoded", urlencoded: [{ key: "n", value: 2 }] } }, () => {});
`})
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	if len(sent) != 2 || sent[0].Body == nil || sent[0].Body.Raw != `{"id":1}` {
		t.Fatalf("sent = %+v", sent)
	}
	if b := sent[1].Body; b == nil || b.Mode != "urlencoded" || len(b.URLEncoded) != 1 || b.URLEncoded[0].Value != "2" {
		t.Errorf("urlencoded body = %+v", b)
	}
}

func TestMemoryLimit(t *testing.T) {
	s := session(t, scripts.Options{MaxMemory: 64 << 20, Timeout: 20 * time.Second})
	start := time.Now()
	_, err := s.Run(context.Background(), scripts.Input{Event: "test", Info: scripts.Info{RequestName: "bomb"},
		Source: `const keep = []; while (true) keep.push(new Array(100000).fill("x"));`})
	if !errors.Is(err, scripts.ErrMemory) {
		t.Fatalf("error = %v, want ErrMemory", err)
	}
	t.Logf("stopped after %v", time.Since(start))
	res := run(t, s, scripts.Input{Event: "test", Source: "pm.test('after', () => {})"})
	passed(t, res, "after")
}

// A deadline that fires as a script ends must not fail the next script in
// the session.
func TestLateInterruptsDontLeak(t *testing.T) {
	// The session's own limit is generous; each edge script gets a 3 ms
	// deadline from its context instead, so only the edge scripts race it.
	s := session(t, scripts.Options{Timeout: 10 * time.Second})
	for i := range 300 {
		// Ends right around its deadline: either outcome is fine.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Millisecond)
		_, err := s.Run(ctx, scripts.Input{Event: "test", Info: scripts.Info{RequestName: "edge"},
			Source: "const t = Date.now(); while (Date.now() - t < 3) {}"})
		cancel()
		if err != nil && !errors.Is(err, scripts.ErrTimeout) {
			t.Fatal(err)
		}
		// Has 10 seconds, so a timeout here can only be the edge script's.
		res, err := s.Run(context.Background(), scripts.Input{Event: "test", Info: scripts.Info{RequestName: "quick"},
			Source: "tests['quick'] = true;"})
		if err != nil {
			t.Fatalf("run %d: a quick script failed after a script at its deadline: %v", i, err)
		}
		passed(t, res, "quick")
	}
}
