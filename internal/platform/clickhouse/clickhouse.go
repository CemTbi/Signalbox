// SPDX-License-Identifier: Apache-2.0

// Package clickhouse connects signalbox-server to ClickHouse, which holds
// what Signalbox observes: spans, check results and request logs. See
// docs/adr/0002-data-stores.md.
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
)

// Conn is a pool of connections to ClickHouse over its native protocol.
type Conn = driver.Conn

// Named passes a query parameter for a {name:Type} placeholder. The server
// receives it apart from the query text, typed, and a time keeps its
// fraction of a second. Prefer it to positional ? arguments, which the
// driver writes into the query and whose times it rounds to the second.
func Named(name string, value any) driver.NamedValue { return ch.Named(name, value) }

// Open creates a connection pool for a URL such as
// clickhouse://user:password@host:9000/database. It doesn't connect; call
// WaitReady before using the pool.
func Open(url string) (Conn, error) {
	opts, err := ch.ParseDSN(url)
	if err != nil {
		// The parse error can quote the URL, password and all.
		return nil, errors.New("read the ClickHouse URL: use clickhouse://user:password@host:9000/database")
	}
	if opts.Compression == nil {
		opts.Compression = &ch.Compression{Method: ch.CompressionLZ4}
	}
	opts.ClientInfo.Products = append(opts.ClientInfo.Products,
		struct{ Name, Version string }{"signalbox-server", buildinfo.Get().Version})
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("create the ClickHouse pool: %w", err)
	}
	return conn, nil
}

// WaitReady pings ClickHouse until it answers, so the server can start
// before ClickHouse does. It gives up when ctx ends, and at once when
// ClickHouse rejects the credentials or doesn't know the database, since
// waiting won't fix either.
func WaitReady(ctx context.Context, conn Conn, logger *slog.Logger) error {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := conn.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if permanent(err) {
			return fmt.Errorf("connect to ClickHouse: %w", err)
		}
		logger.WarnContext(ctx, "waiting for ClickHouse", "attempt", attempt, "error", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return fmt.Errorf("ClickHouse didn't answer in time: %w", err)
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// permanent reports errors that retrying can't fix: wrong credentials, a
// refused address or an unknown database. The codes are from ClickHouse's
// src/Common/ErrorCodes.cpp.
func permanent(err error) bool {
	var ex *ch.Exception
	if !errors.As(err, &ex) {
		return false
	}
	switch ex.Code {
	case 81, // UNKNOWN_DATABASE
		192, // UNKNOWN_USER
		193, // WRONG_PASSWORD
		194, // REQUIRED_PASSWORD
		195, // IP_ADDRESS_NOT_ALLOWED
		497, // ACCESS_DENIED
		516: // AUTHENTICATION_FAILED
		return true
	}
	return false
}

// Check is a readiness check that measures how long ClickHouse takes to
// answer. It isn't critical: without ClickHouse Signalbox can't show
// traces, but everything kept in PostgreSQL still works.
func Check(conn Conn, logger *slog.Logger) health.Check {
	return health.Check{
		Name:          "clickhouse:responseTime",
		ComponentType: "datastore",
		Run: func(ctx context.Context) health.Result {
			start := time.Now()
			if err := conn.Ping(ctx); err != nil {
				// /readyz is public, so the details go to the log only.
				logger.DebugContext(ctx, "ClickHouse health check failed", "error", err)
				return health.Result{Status: health.Fail, Output: "ClickHouse didn't answer"}
			}
			ms := float64(time.Since(start).Microseconds()) / 1000
			return health.Result{Status: health.Pass, ObservedValue: ms, ObservedUnit: "ms"}
		},
	}
}
