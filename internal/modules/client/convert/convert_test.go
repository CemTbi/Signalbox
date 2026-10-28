// SPDX-License-Identifier: Apache-2.0

package convert_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
)

func notes(r convert.Report) map[string][]string {
	out := map[string][]string{}
	for _, n := range r.Notes {
		out[n.Item] = append(out[n.Item], string(n.Level)+": "+n.Message)
	}
	return out
}

func has(list []string, part string) bool {
	for _, s := range list {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestReadPostmanCollection(t *testing.T) {
	data, err := os.ReadFile("testdata/shop.postman_collection.json")
	if err != nil {
		t.Fatal(err)
	}
	c, r, err := convert.ReadPostmanCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Shop API" || c.Description != "Orders and payments." || !strings.Contains(c.Scripts.PreRequest, "pm.variables.set") {
		t.Errorf("collection = %+v", c)
	}
	if want := []convert.Variable{{Key: "baseUrl", Value: "https://api.example.com"}, {Key: "orderId", Value: "42"}}; !reflect.DeepEqual(c.Variables, want) {
		t.Errorf("variables = %+v", c.Variables)
	}
	if r.Folders != 1 || r.Requests != 6 || r.Variables != 2 || r.Clean() {
		t.Errorf("report = %+v", r)
	}

	orders := c.Items[0]
	get, create := orders.Items[0].Request, orders.Items[1].Request
	// Path variables are filled in; the folder's API key is inherited.
	if get.URL != "{{baseUrl}}/orders/{{orderId}}?expand=lines" || *get.Auth != (convert.Auth{Type: "apikey", Key: "X-Api-Key", Value: "{{apiKey}}", In: "header"}) {
		t.Errorf("get = %+v %+v", get, get.Auth)
	}
	if !reflect.DeepEqual(get.Headers, []convert.Header{{Key: "Accept", Value: "application/json", Enabled: true}, {Key: "X-Debug", Value: "1"}}) {
		t.Errorf("headers = %+v", get.Headers)
	}
	if !strings.Contains(orders.Items[0].Scripts.Test, "pm.response.to.have.status") {
		t.Errorf("test script = %q", orders.Items[0].Scripts.Test)
	}
	// A request's own auth wins, and a raw JSON body gets its Content-Type.
	if *create.Auth != (convert.Auth{Type: "basic", Username: "ada", Password: "{{password}}"}) || create.Method != http.MethodPost ||
		!reflect.DeepEqual(create.Headers, []convert.Header{{Key: "Content-Type", Value: "application/json", Enabled: true}}) {
		t.Errorf("create = %+v %+v", create, create.Auth)
	}

	signIn := c.Items[1].Request
	if signIn.Auth != nil || signIn.URL != "https://auth.example.com/token" || signIn.Body != "user=ada+lovelace&pass={{password}}" {
		t.Errorf("sign in = %+v", signIn)
	}
	upload := c.Items[2].Request
	if !strings.Contains(upload.Body, `name="title"`) || strings.Contains(upload.Body, "a.pdf") || !strings.HasPrefix(upload.Headers[0].Value, "multipart/form-data; boundary=signalbox-") {
		t.Errorf("upload = %+v", upload)
	}
	if q := c.Items[4].Request; q.Body != `{"query":"{ order(id: 1) { id } }","variables":{}}` {
		t.Errorf("graphql = %q", q.Body)
	}

	n := notes(r)
	for item, parts := range map[string][]string{
		"":                         {"info: The disabled variable \"old\"", "omitted: The variable \"bad key\"", "info: Scripts are kept"},
		"Orders / Get an order":    {"differs: The test script requires \"cheerio\"", "differs: The test script uses pm.visualizer", "info: 1 saved example"},
		"Orders / Create an order": {"differs: {{$randomEmail}} isn't filled in"},
		"Sign in":                  {"differs: Variables in form fields"},
		"Upload":                   {"omitted: The file field \"file\"", "differs: Postman's \"followRedirects\" setting"},
		"Signed":                   {"omitted: Postman's awsv4 auth"},
	} {
		for _, p := range parts {
			if !has(n[item], p) {
				t.Errorf("%q has no note %q; it has %q", item, p, n[item])
			}
		}
	}
	if has(n["Orders / Create an order"], "$guid") || has(n["Upload"], "disableBodyPruning") {
		t.Errorf("supported things reported: %v", n)
	}
}

func TestUnsupportedAuthIsntReplacedByTheCollections(t *testing.T) {
	data, _ := os.ReadFile("testdata/shop.postman_collection.json")
	c, _, err := convert.ReadPostmanCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	if a := c.Items[3].Request.Auth; a != nil {
		t.Errorf("signed auth = %+v, want none", a)
	}
}

func TestReadPostmanCollectionRefusesOtherFiles(t *testing.T) {
	for name, data := range map[string]string{
		"not JSON":    "{",
		"v1":          `{"id":"x","name":"Old","requests":[{"url":"https://a.test"}]}`,
		"no schema":   `{"info":{"name":"x"},"item":[]}`,
		"environment": `{"name":"Staging","values":[],"_postman_variable_scope":"environment"}`,
	} {
		if _, _, err := convert.ReadPostmanCollection([]byte(name + data)[len(name):]); !errors.Is(err, convert.ErrNotPostman) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCollectionVariablesAreBounded(t *testing.T) {
	big := strings.Repeat("x", 60<<10)
	vars := make([]string, 40)
	for i := range vars {
		vars[i] = `{"key":"v` + strings.Repeat("a", i) + `","value":"` + big + `"}`
	}
	data := `{"info":{"name":"Big","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[],"variable":[` + strings.Join(vars, ",") + `]}`
	c, r, err := convert.ReadPostmanCollection([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	size := 0
	for _, v := range c.Variables {
		size += len(v.Key) + len(v.Value)
	}
	if size > convert.MaxCollectionVariables || len(c.Variables) == 40 || !has(notes(r)[""], "at most 2 MiB together") {
		t.Errorf("%d variables of %d bytes, notes %v", len(c.Variables), size, notes(r)[""])
	}
}

func TestPostmanV20(t *testing.T) {
	data := `{"info":{"name":"Old","schema":"https://schema.getpostman.com/json/collection/v2.0.0/collection.json"},
		"item":[{"name":"Me","request":{"url":"https://a.test/me","method":"GET","header":"Accept: */*\nX-A: 1",
		"auth":{"type":"bearer","bearer":{"token":"abc"}}}},{"name":"Bare","request":"https://a.test/bare"}]}`
	c, r, err := convert.ReadPostmanCollection([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	me := c.Items[0].Request
	if me.Auth == nil || me.Auth.Token != "abc" || len(me.Headers) != 2 || me.Headers[1].Value != "1" {
		t.Errorf("me = %+v", me)
	}
	if bare := c.Items[1].Request; bare.Method != http.MethodGet || bare.URL != "https://a.test/bare" {
		t.Errorf("bare = %+v", bare)
	}
	if !r.Clean() {
		t.Errorf("report = %+v", r.Notes)
	}
}

func TestExportReadsBack(t *testing.T) {
	data, _ := os.ReadFile("testdata/shop.postman_collection.json")
	c, _, err := convert.ReadPostmanCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := convert.ExportPostman("8d1c3a52-6b1e-4f8e-9a1c-2f3b4c5d6e7f", c)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil || doc["info"].(map[string]any)["schema"] != "https://schema.getpostman.com/json/collection/v2.1.0/collection.json" {
		t.Fatalf("export = %s, %v", out, err)
	}
	back, r, err := convert.ReadPostmanCollection(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, c) {
		t.Errorf("round trip changed the collection:\n%+v\n%+v", back, c)
	}
	// What was converted exports cleanly, apart from the script notes.
	for _, n := range r.Notes {
		if n.Level != convert.Info && !strings.Contains(n.Message, "script") && !strings.Contains(n.Message, "randomEmail") && !strings.Contains(n.Message, "form fields") {
			t.Errorf("export reads back with %+v", n)
		}
	}
}

func TestReadPostmanEnvironment(t *testing.T) {
	data := `{"name":"Staging","_postman_variable_scope":"environment","values":[
		{"key":"baseUrl","value":"https://staging.example.com","enabled":true},
		{"key":"token","value":"s3cret","type":"secret","enabled":true},
		{"key":"off","value":"1","enabled":false},
		{"key":"$bad","value":"1"},
		{"key":"baseUrl","value":"https://override.example.com"}]}`
	env, r, err := convert.ReadPostmanEnvironment([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []convert.Variable{{Key: "baseUrl", Value: "https://override.example.com"}, {Key: "token", Value: "s3cret", Secret: true}}
	if env.Name != "Staging" || !reflect.DeepEqual(env.Variables, want) || r.Variables != 2 || r.Clean() {
		t.Errorf("env = %+v, report = %+v", env, r)
	}
	if _, _, err := convert.ReadPostmanEnvironment([]byte(`{"info":{"schema":"x"},"item":[]}`)); !errors.Is(err, convert.ErrNotPostman) {
		t.Errorf("collection as environment: %v", err)
	}
}

func TestReadCurl(t *testing.T) {
	cases := []struct {
		name, command string
		want          convert.Request
		notes         []string
	}{
		{
			name:    "a browser's Copy as cURL (bash)",
			command: "curl 'https://api.example.com/orders?page=2' \\\n  -H 'accept: application/json' \\\n  -H $'x-note: it\\'s' \\\n  --data-raw '{\"sku\":\"A-1\"}' \\\n  --compressed",
			want: convert.Request{Method: "POST", URL: "https://api.example.com/orders?page=2", Body: `{"sku":"A-1"}`, Headers: []convert.Header{
				{Key: "accept", Value: "application/json", Enabled: true}, {Key: "x-note", Value: "it's", Enabled: true},
				{Key: "Content-Type", Value: "application/x-www-form-urlencoded", Enabled: true},
			}},
		},
		{
			name:    "a browser's Copy as cURL (cmd)",
			command: "curl ^\"https://api.example.com/me^\" ^\n  -H ^\"authorization: Bearer abc^\" ^\n  -X PUT",
			want:    convert.Request{Method: "PUT", URL: "https://api.example.com/me", Headers: []convert.Header{{Key: "authorization", Value: "Bearer abc", Enabled: true}}},
		},
		{
			name:    "combined flags, basic auth and -G",
			command: `curl -sSL -XGET -u ada:pw -G -d q=a+b --data-urlencode "name=Ada L" api.example.com/search -k`,
			want: convert.Request{Method: "GET", URL: "http://api.example.com/search?q=a+b&name=Ada+L", Headers: []convert.Header{},
				Auth: &convert.Auth{Type: "basic", Username: "ada", Password: "pw"}},
			notes: []string{"info: The URL has no scheme", "differs: curl's -k option"},
		},
		{
			name:    "a form with a file, and JSON",
			command: `curl -F title=Invoice -F file=@a.pdf --oauth2-bearer t0k -H "Authorization: Basic x" https://a.test/up`,
			notes:   []string{"omitted: The form field \"file\""},
		},
		{
			name:    "negative data isn't an option",
			command: `curl -d -1 https://a.test/n`,
			want: convert.Request{Method: "POST", URL: "https://a.test/n", Body: "-1", Headers: []convert.Header{
				{Key: "Content-Type", Value: "application/x-www-form-urlencoded", Enabled: true}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, r, err := convert.ReadCurl(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want.URL != "" && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v %+v\nwant %+v", got, got.Auth, tc.want)
			}
			n := notes(r)[""]
			for _, p := range tc.notes {
				if !has(n, p) {
					t.Errorf("no note %q in %q", p, n)
				}
			}
			if len(tc.notes) == 0 && len(n) > 0 {
				t.Errorf("notes = %q", n)
			}
		})
	}
	form, _, _ := convert.ReadCurl(`curl -F title=Invoice -F file=@a.pdf --oauth2-bearer t0k -H "Authorization: Basic x" https://a.test/up`)
	if form.Method != http.MethodPost || form.Auth.Token != "t0k" || len(form.Headers) != 1 || !strings.Contains(form.Body, "Invoice") {
		t.Errorf("form = %+v", form)
	}
	// Bash's $'…' escapes, as a browser writes them, and inline data keeps
	// its line breaks.
	escaped, _, err := convert.ReadCurl(`curl https://a.test/ --data-raw $'{"a":"hi\u0021 \x41\101\q\n"}' -d $'line1\nline2'`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"a\":\"hi! AA\\q\n\"}&line1\nline2"; escaped.Body != want {
		t.Errorf("escaped body = %q, want %q", escaped.Body, want)
	}
	for _, bad := range []string{"curl", "curl -H", "curl 'https://a.test"} {
		if _, _, err := convert.ReadCurl(bad); !errors.Is(err, convert.ErrNotCurl) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
