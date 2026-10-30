// SPDX-License-Identifier: Apache-2.0

package clickhouse

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the SQL migrations for ClickHouse, in goose's format.
// ClickHouse has no transactions for DDL, so each file says NO
// TRANSACTION, and its statements must be safe to run again, as IF NOT
// EXISTS makes them: a file that fails halfway is applied again in full.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err) // the embedded directory always exists
	}
	return sub
}

// Locker runs f while no other process migrates. ClickHouse has no
// advisory locks, so the server passes one of PostgreSQL's.
type Locker func(ctx context.Context, f func() error) error

// Migrate applies every pending migration to the database in url, holding
// lock while it does. A nil lock is for tools and tests that run alone.
func Migrate(ctx context.Context, url string, lock Locker, logger *slog.Logger) error {
	opts, err := ch.ParseDSN(url)
	if err != nil {
		return errors.New("read the ClickHouse URL: use clickhouse://user:password@host:9000/database")
	}
	db := ch.OpenDB(opts)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectClickHouse, db, Migrations(), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("load ClickHouse migrations: %w", err)
	}
	if lock == nil {
		lock = func(_ context.Context, f func() error) error { return f() }
	}
	return lock(ctx, func() error {
		results, err := provider.Up(ctx)
		for _, r := range results {
			logger.InfoContext(ctx, "applied ClickHouse migration",
				"version", r.Source.Version, "file", r.Source.Path, "duration_ms", float64(r.Duration.Microseconds())/1000)
		}
		if err != nil {
			return fmt.Errorf("migrate ClickHouse: %w", err)
		}
		version, err := provider.GetDBVersion(ctx)
		if err != nil {
			return fmt.Errorf("read the ClickHouse schema version: %w", err)
		}
		logger.InfoContext(ctx, "ClickHouse schema is current", "version", version)
		return nil
	})
}
