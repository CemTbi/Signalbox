// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func TestIngestKeys(t *testing.T) {
	pool := postgrestest.New(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	ws := workspaces.New(pool, log)
	user := func(name string) uuid.UUID {
		u := id.New()
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, u, name+"@example.com", name); err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, editor, stranger := user("ada"), user("bob"), user("eve")
	w, err := ws.CreateWorkspace(ctx, owner, workspaces.WorkspaceInput{Name: "Shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'editor')`, w.ID, editor); err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProject(ctx, owner, w.ID, workspaces.ProjectInput{Name: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	envs, err := ws.ListEnvironments(ctx, owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	env := envs[0].ID
	keys := telemetry.NewKeys(pool, log, ws)

	// Owners and admins create keys; editors can't, strangers can't see.
	if _, err := keys.Create(ctx, editor, env, "checkout"); !errors.Is(err, telemetry.ErrForbidden) {
		t.Errorf("editor creates: %v", err)
	}
	if _, err := keys.Create(ctx, stranger, env, "checkout"); !errors.Is(err, telemetry.ErrNotFound) {
		t.Errorf("stranger creates: %v", err)
	}
	if _, err := keys.Create(ctx, owner, env, " "); !errors.As(err, new(validate.Errors)) {
		t.Errorf("no name: %v", err)
	}
	k, err := keys.Create(ctx, owner, env, " checkout-service ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Key, telemetry.IngestKeyPrefix) || k.Name != "checkout-service" || k.Hint != k.Key[len(k.Key)-4:] {
		t.Errorf("key = %+v", k)
	}

	// The key sends to its environment, and records its use.
	s, err := keys.Authenticate(ctx, k.Key)
	if err != nil || s.EnvironmentID != env || s.KeyID != k.ID {
		t.Fatalf("authenticate = %+v, %v", s, err)
	}
	for _, bad := range []string{"", "sbx_ing_short", "sbx_pat_" + k.Key[8:], k.Key[:len(k.Key)-1] + "x"} {
		if _, err := keys.Authenticate(ctx, bad); !errors.Is(err, telemetry.ErrUnknownKey) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	list, err := keys.List(ctx, editor, env)
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil || list[0].CreatedBy != owner {
		t.Errorf("list = %+v, %v", list, err)
	}
	if _, err := keys.List(ctx, stranger, env); !errors.Is(err, telemetry.ErrNotFound) {
		t.Errorf("stranger lists: %v", err)
	}

	// Revoking stops the key at once in this process.
	if err := keys.Revoke(ctx, editor, k.ID); !errors.Is(err, telemetry.ErrForbidden) {
		t.Errorf("editor revokes: %v", err)
	}
	if err := keys.Revoke(ctx, owner, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Authenticate(ctx, k.Key); !errors.Is(err, telemetry.ErrUnknownKey) {
		t.Errorf("a revoked key: %v", err)
	}
	if err := keys.Revoke(ctx, owner, k.ID); !errors.Is(err, telemetry.ErrNotFound) {
		t.Errorf("revoked twice: %v", err)
	}
}

func TestPrepareRedactsAndLimits(t *testing.T) {
	p := telemetry.NewPreparer()
	s := telemetry.Span{
		Name: "GET",
		Kind: telemetry.KindServer,
		Attributes: map[string]string{
			"http.request.method":               "GET",
			"http.route":                        "/orders/{id}",
			"http.status_code":                  "404",
			"http.request.header.authorization": "Bearer abc",
			"http.response.header.set-cookie":   "session=1",
			"app.api_key":                       "k",
			"gen_ai.usage.input_tokens":         "42",
			"url.full":                          "https://shop.example/orders/7?id=7&access_token=t0k&key=k3y#top",
			"url.query":                         "page=2&apiKey=x",
			"db.statement":                      "SELECT 1",
			"big":                               strings.Repeat("é", telemetry.MaxAttributeValue),
			strings.Repeat("k", 2000):           "a name too long",
		},
		Events: make([]telemetry.Event, telemetry.MaxEvents+3),
	}
	d := p.Prepare(&s)
	want := map[string]string{
		"http.request.header.authorization": telemetry.Redacted,
		"http.response.header.set-cookie":   telemetry.Redacted,
		"app.api_key":                       telemetry.Redacted,
		"gen_ai.usage.input_tokens":         "42",
		"url.full":                          "https://shop.example/orders/7?id=7&access_token=" + telemetry.Redacted + "&key=" + telemetry.Redacted + "#top",
		"url.query":                         "page=2&apiKey=" + telemetry.Redacted,
		"db.statement":                      "SELECT 1",
	}
	for k, v := range want {
		if s.Attributes[k] != v {
			t.Errorf("%s = %q, want %q", k, s.Attributes[k], v)
		}
	}
	if big := s.Attributes["big"]; len(big) > telemetry.MaxAttributeValue || !strings.HasPrefix(big, "é") || !strings.HasSuffix(big, "é") {
		t.Errorf("a long value was cut to %d bytes, mid-character: %v", len(big), !strings.HasSuffix(big, "é"))
	}
	if d.Events != 3 || len(s.Events) != telemetry.MaxEvents || d.Truncated != 1 || d.Attributes != 1 {
		t.Errorf("dropped = %+v", d)
	}
	// Old attribute names fill the fields too, and a route names the
	// operation.
	if s.HTTPMethod != "GET" || s.HTTPStatusCode != 404 || s.Operation != "GET /orders/{id}" || s.Service != telemetry.UnknownService {
		t.Errorf("span = %s %d %q %q", s.HTTPMethod, s.HTTPStatusCode, s.Operation, s.Service)
	}

	many := map[string]string{}
	for i := range telemetry.MaxAttributes + 5 {
		many[fmt.Sprintf("a%03d", i)] = "v"
	}
	s = telemetry.Span{Name: "work", Attributes: many}
	if d := p.Prepare(&s); d.Attributes != 5 || len(s.Attributes) != telemetry.MaxAttributes || s.Attributes["a000"] == "" {
		t.Errorf("dropped %+v, kept %d", d, len(s.Attributes))
	}
}

func TestOperationsFoldPastTheLimit(t *testing.T) {
	p := telemetry.NewPreparer()
	env := id.New()
	name := func(i int) string {
		s := telemetry.Span{EnvironmentID: env, Service: "web", Name: fmt.Sprint("GET /p/", i)}
		p.Prepare(&s)
		return s.Operation
	}
	for i := range telemetry.MaxOperations {
		if got := name(i); got != fmt.Sprint("GET /p/", i) {
			t.Fatalf("operation %d = %q", i, got)
		}
	}
	if got := name(telemetry.MaxOperations); got != telemetry.OtherOperation {
		t.Errorf("past the limit = %q", got)
	}
	// Known operations keep their names, and other services have their own.
	if got := name(7); got != "GET /p/7" {
		t.Errorf("a known operation = %q", got)
	}
	s := telemetry.Span{EnvironmentID: env, Service: "api", Name: "GET /p/5000"}
	p.Prepare(&s)
	if s.Operation != "GET /p/5000" {
		t.Errorf("another service = %q", s.Operation)
	}
}

func TestWriterBatchesAndAcknowledges(t *testing.T) {
	conn := newStore(t)
	ctx := context.Background()
	reg := metrics.NewRegistry()
	w := telemetry.NewWriter(conn, slog.New(slog.DiscardHandler), telemetry.WriterOptions{MaxBatch: 50, Interval: 50 * time.Millisecond, MaxPending: 100}, reg)
	env := id.New()
	span := func(i int) telemetry.Span {
		return telemetry.Span{EnvironmentID: env, TraceID: traceID(byte(i)), SpanID: spanID(byte(i)), Start: recently(), Duration: time.Millisecond,
			Service: "web", Name: "GET /", Kind: telemetry.KindServer}
	}
	// Ten senders at once; each returns once its spans are stored.
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			if err := w.Write(ctx, []telemetry.Span{span(2*i + 1), span(2*i + 2)}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var n uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM spans`).Scan(&n); err != nil || n != 20 {
		t.Errorf("stored %d spans, %v", n, err)
	}
	// Past MaxPending, it refuses at once.
	if err := w.Write(ctx, make([]telemetry.Span, 101)); !errors.Is(err, telemetry.ErrBusy) {
		t.Errorf("too many: %v", err)
	}
	w.Close()
	if err := w.Write(ctx, []telemetry.Span{span(99)}); !errors.Is(err, telemetry.ErrClosed) {
		t.Errorf("after close: %v", err)
	}
	var b bytes.Buffer
	reg.WriteTo(&b)
	for _, want := range []string{"signalbox_ingest_spans_written_total 20\n", "signalbox_ingest_spans_pending 0\n"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("lacks %s:\n%s", want, b.String())
		}
	}
}
