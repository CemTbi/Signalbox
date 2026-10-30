// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Advisory lock keys, one per job that processes take turns at. goose
// uses its own key for PostgreSQL's migrations.
const (
	// LockClickHouseMigrations serializes ClickHouse migrations.
	LockClickHouseMigrations int64 = 0x5167_6E61_6C62_0001
)

// WithAdvisoryLock runs f while holding the session advisory lock key,
// waiting for another process to release it first. The lock is released
// when f returns, or when the connection ends if releasing fails.
func WithAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, key int64, f func() error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("take a connection for a lock: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		// A cancelled wait can leave the connection in any state.
		_ = conn.Hijack().Close(context.Background()) // closing is the cleanup
		return fmt.Errorf("wait for the lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			// Ending the session releases its locks.
			_ = conn.Hijack().Close(unlockCtx)
			return
		}
		conn.Release()
	}()
	return f()
}
