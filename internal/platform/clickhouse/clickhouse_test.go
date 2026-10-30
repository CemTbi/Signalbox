// SPDX-License-Identifier: Apache-2.0

package clickhouse_test

import (
	"context"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
)

func TestOpenDoesntRepeatTheURL(t *testing.T) {
	_, err := clickhouse.Open("clickhouse://user:s3cret@host:9000/db?dial_timeout=banana")
	if err == nil {
		t.Fatal("Open accepted a URL with a bad parameter")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the error shows the password: %v", err)
	}
}

func TestWaitReadyAndCheck(t *testing.T) {
	conn := clickhousetest.New(t)
	ctx := context.Background()
	if err := clickhouse.WaitReady(ctx, conn, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	check := clickhouse.Check(conn, slog.New(slog.DiscardHandler))
	if check.Critical {
		t.Error("ClickHouse is critical for readiness, but Signalbox works without it")
	}
	if r := check.Run(ctx); r.Status != health.Pass {
		t.Errorf("check = %+v, want pass", r)
	}
}

func TestWaitReadyGivesUpOnAWrongPassword(t *testing.T) {
	_, dbURL := clickhousetest.NewWithURL(t)
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(u.User.Username(), "wrong")
	conn, err := clickhouse.Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	if err := clickhouse.WaitReady(ctx, conn, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("WaitReady succeeded with a wrong password")
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("WaitReady retried a wrong password for %s", waited)
	}
}

func TestMigrate(t *testing.T) {
	conn, dbURL := clickhousetest.NewWithURL(t)
	ctx := context.Background()
	locked := 0
	lock := func(_ context.Context, f func() error) error {
		locked++
		return f()
	}
	// Twice: the second run finds nothing to do.
	for range 2 {
		if err := clickhouse.Migrate(ctx, dbURL, lock, slog.New(slog.DiscardHandler)); err != nil {
			t.Fatal(err)
		}
	}
	if locked != 2 {
		t.Errorf("migrated %d times under the lock, want 2", locked)
	}
	var tables []string
	rows, err := conn.Query(ctx, `SELECT name FROM system.tables WHERE database = currentDatabase() ORDER BY name`)
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
	if got := strings.Join(tables, " "); got != "goose_db_version span_metrics_1m span_metrics_1m_mv spans trace_ids trace_ids_mv" {
		t.Errorf("tables = %s", got)
	}
	files, err := fs.Glob(clickhouse.Migrations(), "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	var applied uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM goose_db_version WHERE version_id > 0`).Scan(&applied); err != nil || applied != uint64(len(files)) {
		t.Errorf("recorded %d migrations, want %d: %v", applied, len(files), err)
	}
	if err := clickhouse.Migrate(ctx, "clickhouse://u:s3cret@[::1/db", nil, slog.New(slog.DiscardHandler)); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("a bad URL: %v", err)
	}
}
