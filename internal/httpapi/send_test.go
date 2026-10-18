// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestSendingRequests(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Seen", r.Method+" "+r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(append([]byte(`{"got":`), append(body, '}')...))
	}))
	defer target.Close()
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	send := "/api/v1/projects/" + p.ID + "/send"

	r := h.do(call{method: http.MethodPost, path: send, body: map[string]any{
		"method": "POST", "url": target.URL + "/orders", "body": `{"sku":"A-1"}`,
		"headers": []map[string]string{{"key": "Authorization", "value": "Bearer secret"}, {"key": "Content-Type", "value": "application/json"}},
	}})
	r.expect(t, http.StatusOK, "")
	var sent httpapi.HistoryEntryJSON
	r.decode(t, &sent)
	if sent.Response == nil || sent.Response.Status != 201 || sent.Response.Body != `{"got":{"sku":"A-1"}}` || sent.Response.BodyEncoding != "text" ||
		sent.Timings.Total <= 0 || sent.Failure != nil {
		t.Fatalf("sent = %+v", sent)
	}
	seen := ""
	for _, hd := range sent.Response.Headers {
		if hd.Key == "X-Seen" {
			seen = hd.Value
		}
	}
	// The request went out with its credentials; history shows •••.
	if seen != "POST Bearer secret" || sent.Request.Headers[0].Value != "•••" || r.header.Get("Location") != "/api/v1/history/"+sent.ID {
		t.Errorf("seen %q, stored headers %+v, Location %q", seen, sent.Request.Headers, r.header.Get("Location"))
	}

	var page struct {
		Data []httpapi.HistorySummaryJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/history"}).decode(t, &page)
	if len(page.Data) != 1 || page.Data[0].ID != sent.ID || page.Data[0].Status != 201 || page.Data[0].Method != http.MethodPost {
		t.Errorf("history = %+v", page)
	}
	var again httpapi.HistoryEntryJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/history/" + sent.ID}).decode(t, &again)
	if again.Response == nil || again.Response.Body != sent.Response.Body || again.Request.Body != `{"sku":"A-1"}` {
		t.Errorf("entry = %+v", again)
	}

	// The runner never reaches cloud metadata endpoints. The send works;
	// the result says why nothing came back.
	r = h.do(call{method: http.MethodPost, path: send, body: map[string]any{"method": "GET", "url": "http://169.254.169.254/latest/meta-data/"}})
	var blocked httpapi.HistoryEntryJSON
	r.decode(t, &blocked)
	if blocked.Failure == nil || blocked.Failure.Code != "blocked" || blocked.Response != nil {
		t.Errorf("metadata endpoint = %+v", blocked)
	}

	h.do(call{method: http.MethodPost, path: send, body: map[string]any{"url": target.URL}, invalid: true}).
		expect(t, http.StatusBadRequest, "validation-failed")
	// Sizes past the contract's are refused, so one send can't fill history.
	r = h.do(call{method: http.MethodPost, path: send, invalid: true, body: map[string]any{
		"method": "GET", "url": target.URL + "/" + strings.Repeat("a", 9000),
		"headers": []map[string]string{{"key": strings.Repeat("k", 1001), "value": "v"}},
	}})
	r.expect(t, http.StatusBadRequest, "validation-failed")
	for _, pointer := range []string{`"/url"`, `"/headers/0/key"`} {
		if !strings.Contains(string(r.body), pointer) {
			t.Errorf("no error at %s: %s", pointer, r.body)
		}
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/history/hist_01j9z3w5y7a9c1e3g5j7m9p1r3"}).expect(t, http.StatusNotFound, "not-found")
}
