// SPDX-License-Identifier: Apache-2.0

// Package clickhousetest gives tests their own, empty ClickHouse database.
//
// The first test in a package starts ClickHouse in a container through
// Testcontainers, or uses the server in SIGNALBOX_TEST_CLICKHOUSE_URL. Each
// test then gets a new database, dropped when the test ends.
//
// Without Docker and without SIGNALBOX_TEST_CLICKHOUSE_URL the tests skip,
// unless SIGNALBOX_REQUIRE_DATABASE_TESTS is set, as it is in CI.
package clickhousetest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/containertest"
)

// Image is the ClickHouse version tests run against. Keep it in step with
// deploy/compose/compose.yaml.
const Image = "clickhouse/clickhouse-server:26.8"

var (
	once    sync.Once
	server  string // URL of the server's default database
	skip    string // why the tests can't run, if they can't
	failure error
)

// New returns a connection whose default database is new and empty, and
// is dropped when the test ends.
func New(tb testing.TB) clickhouse.Conn {
	tb.Helper()
	conn, _ := NewWithURL(tb)
	return conn
}

// NewWithURL is New, and also returns the database's URL for code that
// opens its own connection.
func NewWithURL(tb testing.TB) (clickhouse.Conn, string) {
	tb.Helper()
	once.Do(start)
	if skip != "" {
		if os.Getenv(containertest.RequireEnv) != "" {
			tb.Fatalf("database tests are required but can't run: %s", skip)
		}
		tb.Skip(skip)
	}
	if failure != nil {
		tb.Fatal(failure)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := clickhouse.Open(server)
	if err != nil {
		tb.Fatal(err)
	}
	defer admin.Close()
	name := "test_" + strings.ToLower(rand.Text()[:16])
	if err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		tb.Fatalf("create test database: %v", err)
	}
	dbURL := withDatabase(server, name)
	conn, err := clickhouse.Open(dbURL)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		_ = conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		admin, err := clickhouse.Open(server)
		if err != nil {
			tb.Errorf("drop test database: %v", err)
			return
		}
		defer admin.Close()
		if err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" SYNC"); err != nil {
			tb.Errorf("drop test database: %v", err)
		}
	})
	return conn, dbURL
}

func start() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	server = os.Getenv("SIGNALBOX_TEST_CLICKHOUSE_URL")
	if server != "" {
		return
	}
	if reason := containertest.DockerUnavailable(ctx); reason != "" {
		skip = "no ClickHouse for database tests: start Docker or set SIGNALBOX_TEST_CLICKHOUSE_URL (" + reason + ")"
		return
	}
	ctr, err := containertest.Run(ctx, func(ctx context.Context) (*tcclickhouse.ClickHouseContainer, error) {
		return tcclickhouse.Run(ctx, Image)
	})
	if err != nil {
		failure = fmt.Errorf("start ClickHouse with Testcontainers: %w", err)
		return
	}
	// Testcontainers' reaper removes the container when the test binary exits.
	if server, err = ctr.ConnectionString(ctx); err != nil {
		failure = fmt.Errorf("read the container's connection string: %w", err)
	}
}

// withDatabase returns the URL with its database name replaced.
func withDatabase(dbURL, name string) string {
	u, err := url.Parse(dbURL)
	if err != nil {
		panic(fmt.Sprintf("clickhousetest: ClickHouse URL isn't a URL: %v", err))
	}
	u.Path = "/" + name
	return u.String()
}
