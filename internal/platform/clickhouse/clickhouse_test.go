// SPDX-License-Identifier: Apache-2.0

package clickhouse_test

import (
	"context"
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
