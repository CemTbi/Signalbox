// SPDX-License-Identifier: Apache-2.0

// Package postgres connects signalbox-server to PostgreSQL and keeps the
// schema current. See docs/adr/0002-data-stores.md and docs/adr/0009-database-conventions.md.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/CemTbi/Signalbox/internal/health"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the schema migrations, in the order they apply.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	return sub
}

// Open creates a connection pool for the database URL. It doesn't connect;
// call WaitReady before using the pool.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// pgconn redacts the password in its parse errors.
		return nil, fmt.Errorf("read the database URL: %w", err)
	}
	if cfg.ConnConfig.RuntimeParams["application_name"] == "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = "signalbox-server"
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create the database pool: %w", err)
	}
	return pool, nil
}

// WaitReady pings the database until it answers, so the server can start
// before the database does. It gives up when ctx ends, and at once when the
// database rejects the credentials or doesn't exist, since waiting won't fix
// either.
func WaitReady(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if permanent(err) {
			return fmt.Errorf("connect to the database: %w", err)
		}
		logger.WarnContext(ctx, "waiting for the database", "attempt", attempt, "error", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return fmt.Errorf("the database didn't answer in time: %w", err)
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// permanent reports errors that retrying can't fix: wrong credentials, a
// missing database or a missing role.
func permanent(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "28000", "28P01", "3D000": // invalid authorization, invalid password, unknown database
		return true
	}
	return false
}

// Migrate applies every pending migration. An advisory lock makes processes
// that start at the same time take turns, so each migration runs once.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() // leaves the pool open

	// Poll for the lock every second, for up to 10 minutes.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 600))
	if err != nil {
		return fmt.Errorf("create the migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, Migrations(),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	for _, r := range results {
		logger.InfoContext(ctx, "applied database migration",
			"version", r.Source.Version, "file", r.Source.Path, "duration_ms", float64(r.Duration.Microseconds())/1000)
	}
	if err != nil {
		return fmt.Errorf("migrate the database: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	logger.InfoContext(ctx, "database schema is current", "version", version)
	return nil
}

// Check is a critical readiness check that measures how long PostgreSQL
// takes to answer.
func Check(pool *pgxpool.Pool, logger *slog.Logger) health.Check {
	return health.Check{
		Name:          "postgres:responseTime",
		ComponentType: "datastore",
		Critical:      true,
		Run: func(ctx context.Context) health.Result {
			start := time.Now()
			if err := pool.Ping(ctx); err != nil {
				// /readyz is public, so the details go to the log only.
				logger.DebugContext(ctx, "database health check failed", "error", err)
				return health.Result{Status: health.Fail, Output: "PostgreSQL didn't answer"}
			}
			ms := float64(time.Since(start).Microseconds()) / 1000
			return health.Result{Status: health.Pass, ObservedValue: ms, ObservedUnit: "ms"}
		},
	}
}

// IsUniqueViolation reports whether err is a unique constraint violation on
// the named constraint or index.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// IsForeignKeyViolation reports whether err is a foreign key violation: the
// row refers to one that doesn't exist, often because it was just deleted.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
