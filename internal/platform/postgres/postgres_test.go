// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/platform/postgres"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestMigrateIsIdempotent(t *testing.T) {
	pool := postgrestest.New(t) // already migrated once
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool, discard); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var tables []string
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := "access_tokens agents collection_items collections environment_variable_sets environment_variables environments ingest_keys projects request_history services sessions spec_revisions spec_versions specs sso_attempts test_run_steps test_runs user_identities users workspace_invitations workspace_keys workspace_members workspaces"
	if got := strings.Join(tables, " "); got != want {
		t.Errorf("tables = %s, want %s", got, want)
	}
}

func TestMigrationsRunConcurrently(t *testing.T) {
	_, url := postgrestest.NewWithURL(t)
	ctx := context.Background()
	errs := make(chan error, 3)
	for range 3 {
		go func() {
			pool, err := postgres.Open(ctx, url)
			if err != nil {
				errs <- err
				return
			}
			defer pool.Close()
			errs <- postgres.Migrate(ctx, pool, discard)
		}()
	}
	for range 3 {
		if err := <-errs; err != nil {
			t.Errorf("Migrate: %v", err)
		}
	}
}

func TestCheckReportsResponseTime(t *testing.T) {
	pool := postgrestest.New(t)
	check := postgres.Check(pool, discard)
	if !check.Critical || check.ComponentType != "datastore" {
		t.Errorf("check = %+v, want a critical datastore check", check)
	}
	r := check.Run(context.Background())
	if r.Status != health.Pass || r.ObservedUnit != "ms" {
		t.Errorf("result = %+v, want pass in ms", r)
	}

	pool.Close()
	if r := check.Run(context.Background()); r.Status != health.Fail || strings.Contains(r.Output, "closed") {
		t.Errorf("after close: result = %+v, want a fail without internal details", r)
	}
}

func TestWaitReadyStopsOnWrongPassword(t *testing.T) {
	_, url := postgrestest.NewWithURL(t)
	bad := strings.Replace(url, ":postgres@", ":wrong-password@", 1)
	if bad == url {
		t.Skip("the test database URL has no password to replace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	start := time.Now()
	err = postgres.WaitReady(ctx, pool, discard)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "28P01" {
		t.Fatalf("WaitReady error = %v, want invalid password", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("WaitReady took %s; a wrong password shouldn't be retried", time.Since(start))
	}
}

func TestOpenRejectsMalformedURLWithoutLeakingPassword(t *testing.T) {
	_, err := postgres.Open(context.Background(), "postgres://user:s3cret@host:notaport/db")
	if err == nil {
		t.Fatal("Open accepted a malformed URL")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaks the password: %v", err)
	}
}
