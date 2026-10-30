// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/platform/postgres"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

func TestAdvisoryLockTakesTurns(t *testing.T) {
	pool := postgrestest.New(t)
	ctx := context.Background()
	const key = 42
	var inside, most atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			err := postgres.WithAdvisoryLock(ctx, pool, key, func() error {
				n := inside.Add(1)
				defer inside.Add(-1)
				for {
					m := most.Load()
					if n <= m || most.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d held the lock at once", most.Load())
	}

	// f's error comes back, and the lock is free afterwards.
	boom := errors.New("boom")
	if err := postgres.WithAdvisoryLock(ctx, pool, key, func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	var free bool
	if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(key)).Scan(&free); err != nil || !free {
		t.Errorf("the lock wasn't released: %v %v", free, err)
	}

	// A wait that's cancelled gives up.
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = postgres.WithAdvisoryLock(ctx, pool, key+1, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := postgres.WithAdvisoryLock(short, pool, key+1, func() error { return nil }); err == nil {
		t.Error("a cancelled wait ran f")
	}
	close(release)
}
