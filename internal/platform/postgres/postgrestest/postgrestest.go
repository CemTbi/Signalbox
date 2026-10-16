// SPDX-License-Identifier: Apache-2.0

// Package postgrestest gives tests their own PostgreSQL database with every
// migration applied.
//
// The first test in a package starts PostgreSQL in a container through
// Testcontainers, or uses the server in SIGNALBOX_TEST_DATABASE_URL, and
// migrates a template database once. Each test then gets a copy of the
// template, which takes milliseconds, and the copy is dropped when the test
// ends.
//
// Without Docker and without SIGNALBOX_TEST_DATABASE_URL the tests skip,
// unless SIGNALBOX_REQUIRE_DATABASE_TESTS is set, as it is in CI.
package postgrestest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/CemTbi/Signalbox/internal/platform/postgres"
)

// Image is the PostgreSQL version tests run against. Keep it in step with
// deploy/compose/compose.yaml.
const Image = "postgres:18-alpine"

var (
	once     sync.Once
	server   string // URL of the server's maintenance database
	template string // name of the migrated template database
	skip     string // why the tests can't run, if they can't
	failure  error
)

// New returns a pool connected to a fresh, fully migrated database that is
// dropped when the test ends.
func New(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	pool, _ := NewWithURL(tb)
	return pool
}

// NewWithURL is New, and also returns the database's connection URL for
// code that opens its own pool.
func NewWithURL(tb testing.TB) (*pgxpool.Pool, string) {
	tb.Helper()
	once.Do(start)
	if skip != "" {
		if os.Getenv("SIGNALBOX_REQUIRE_DATABASE_TESTS") != "" {
			tb.Fatalf("database tests are required but can't run: %s", skip)
		}
		tb.Skip(skip)
	}
	if failure != nil {
		tb.Fatal(failure)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name := "test_" + strings.ToLower(rand.Text()[:16])
	if err := exec(ctx, server, "CREATE DATABASE "+ident(name)+" TEMPLATE "+ident(template)); err != nil {
		tb.Fatalf("create test database: %v", err)
	}
	dbURL := withDatabase(server, name)
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		tb.Fatalf("connect to test database: %v", err)
	}
	tb.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := exec(ctx, server, "DROP DATABASE IF EXISTS "+ident(name)+" WITH (FORCE)"); err != nil {
			tb.Errorf("drop test database: %v", err)
		}
	})
	return pool, dbURL
}

func start() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	server = os.Getenv("SIGNALBOX_TEST_DATABASE_URL")
	if server == "" {
		if reason := dockerUnavailable(ctx); reason != "" {
			skip = "no PostgreSQL for database tests: start Docker or set SIGNALBOX_TEST_DATABASE_URL (" + reason + ")"
			return
		}
		ctr, err := runContainer(ctx)
		if err != nil {
			failure = fmt.Errorf("start PostgreSQL with Testcontainers: %w", err)
			return
		}
		// Testcontainers' reaper removes the container when the test binary exits.
		if server, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
			failure = fmt.Errorf("read the container's connection string: %w", err)
			return
		}
	}
	template, failure = migrateTemplate(ctx)
}

// runContainer starts PostgreSQL. The test binaries of one `go test` run
// share Testcontainers' reaper container, and a binary can find it before
// it is running; a short wait and another try fixes that.
func runContainer(ctx context.Context) (*tcpostgres.PostgresContainer, error) {
	var err error
	for attempt := 1; attempt <= 4; attempt++ {
		var ctr *tcpostgres.PostgresContainer
		ctr, err = tcpostgres.Run(ctx, Image, tcpostgres.BasicWaitStrategies())
		if err == nil {
			return ctr, nil
		}
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		if !strings.Contains(err.Error(), "reaper") {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return nil, err
}

// dockerUnavailable returns why Testcontainers can't reach Docker, or "".
func dockerUnavailable(ctx context.Context) (reason string) {
	defer func() {
		if p := recover(); p != nil {
			reason = fmt.Sprint(p)
		}
	}()
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		return err.Error()
	}
	if err := provider.Health(ctx); err != nil {
		return err.Error()
	}
	return ""
}

// migrateTemplate creates the template database for the current set of
// migrations, unless it exists. The name contains a hash of the migrations,
// so test binaries that share a server with SIGNALBOX_TEST_DATABASE_URL
// share the template, and a changed migration gets a new one. An advisory
// lock keeps two binaries from building it at the same time.
func migrateTemplate(ctx context.Context) (string, error) {
	name, err := templateName()
	if err != nil {
		return "", err
	}
	conn, err := pgx.Connect(ctx, server)
	if err != nil {
		return "", fmt.Errorf("connect to PostgreSQL for tests: %w", err)
	}
	defer conn.Close(context.Background())

	const lockID = 7_313_026_402 // arbitrary, unique to this package
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return "", fmt.Errorf("lock template creation: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockID) }()

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return "", fmt.Errorf("look for the template database: %w", err)
	}
	if exists {
		return name, nil
	}

	building := name + "_building"
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident(building)+" WITH (FORCE)"); err != nil {
		return "", fmt.Errorf("remove a half-built template: %w", err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+ident(building)); err != nil {
		return "", fmt.Errorf("create the template database: %w", err)
	}
	pool, err := pgxpool.New(ctx, withDatabase(server, building))
	if err != nil {
		return "", fmt.Errorf("connect to the template database: %w", err)
	}
	err = postgres.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pool.Close()
	if err != nil {
		return "", fmt.Errorf("migrate the template database: %w", err)
	}
	// Renaming last means a template exists only once it is complete.
	if _, err := conn.Exec(ctx, "ALTER DATABASE "+ident(building)+" RENAME TO "+ident(name)); err != nil {
		return "", fmt.Errorf("finish the template database: %w", err)
	}
	return name, nil
}

func templateName() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(postgres.Migrations(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(postgres.Migrations(), path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(data))
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash the migrations: %w", err)
	}
	return "signalbox_template_" + hex.EncodeToString(h.Sum(nil))[:12], nil
}

func exec(ctx context.Context, dbURL, sql string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	_, err = conn.Exec(ctx, sql)
	return err
}

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

// withDatabase returns the URL with its database name replaced.
func withDatabase(dbURL, name string) string {
	u, err := url.Parse(dbURL)
	if err != nil {
		panic(fmt.Sprintf("postgrestest: database URL isn't a URL: %v", err))
	}
	u.Path = "/" + name
	return u.String()
}
