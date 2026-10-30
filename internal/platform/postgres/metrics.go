// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

// RegisterMetrics reports the connection pool's size and how long work
// waits for a connection, read from the pool at each scrape.
func RegisterMetrics(reg *metrics.Registry, pool *pgxpool.Pool) {
	stat := func(f func(s *pgxpool.Stat) float64) func() float64 {
		return func() float64 { return f(pool.Stat()) }
	}
	reg.NewGaugeFunc("signalbox_db_connections_max", "The most connections the PostgreSQL pool opens.",
		stat(func(s *pgxpool.Stat) float64 { return float64(s.MaxConns()) }))
	reg.NewGaugeFunc("signalbox_db_connections_in_use", "PostgreSQL connections in use.",
		stat(func(s *pgxpool.Stat) float64 { return float64(s.AcquiredConns()) }))
	reg.NewGaugeFunc("signalbox_db_connections_idle", "PostgreSQL connections open and idle.",
		stat(func(s *pgxpool.Stat) float64 { return float64(s.IdleConns()) }))
	reg.NewCounterFunc("signalbox_db_acquires_total", "Connections taken from the PostgreSQL pool.",
		stat(func(s *pgxpool.Stat) float64 { return float64(s.AcquireCount()) }))
	reg.NewCounterFunc("signalbox_db_acquire_waits_total", "Connections taken from the PostgreSQL pool after waiting, because none was free.",
		stat(func(s *pgxpool.Stat) float64 { return float64(s.EmptyAcquireCount()) }))
	reg.NewCounterFunc("signalbox_db_acquire_wait_seconds_total", "Time spent taking connections from the PostgreSQL pool, waits included.",
		stat(func(s *pgxpool.Stat) float64 { return s.AcquireDuration().Seconds() }))
}
