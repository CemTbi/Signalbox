// SPDX-License-Identifier: Apache-2.0

// Command spanbench measures span storage in ClickHouse: how fast spans go
// in, how much room they take, and how fast the trace UI's questions are
// answered. It is the tool behind spike 2 and ADR-0014, and isn't shipped.
//
//	spanbench schema                     create the tables
//	spanbench paced -rate 5000 -for 15m  write spans as they happen
//	spanbench bulk -rate 5000 -hours 6   fill hours of history quickly
//	spanbench queries -window 6h         time the trace UI's queries
//	spanbench graph -minutes 10          time building the service graph
//	spanbench stats                      sizes on disk
//	spanbench drop                       remove the tables
//
// Every command reads the ClickHouse URL from -url or SPANBENCH_URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// env is the one environment all generated spans belong to.
var env = uuid.MustParse("0193a6f0-5c2e-7d41-9b8a-3f6c2e1d4a57")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "spanbench:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("name a command: schema, paced, bulk, queries, graph, stats or drop")
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	url := fs.String("url", envOr("SPANBENCH_URL", "clickhouse://bench:bench@127.0.0.1:19000/bench"), "ClickHouse URL")
	seed := fs.Uint64("seed", 1, "random seed for generated spans")
	rate := fs.Int("rate", 5000, "spans per second")
	length := fs.Duration("for", 15*time.Minute, "how long paced writes")
	hours := fs.Float64("hours", 1, "hours of history bulk writes")
	endFlag := fs.String("end", "", "end of the history, RFC 3339 (default: now, rounded down to the minute)")
	workers := fs.Int("workers", 4, "parallel bulk writers")
	batch := fs.Int("batch", 200_000, "spans per bulk insert")
	window := fs.Duration("window", time.Hour, "time range queries search")
	iterations := fs.Int("iterations", 20, "runs of each query after the first")
	minutes := fs.Int("minutes", 10, "minutes of the service graph to build")
	fs.Float64Var(&wildNames, "wild-names", 0, "share of server spans named after their URL path instead of their route")
	if err := fs.Parse(args); err != nil {
		return err
	}
	end := time.Now().UTC().Truncate(time.Minute)
	if *endFlag != "" {
		t, err := time.Parse(time.RFC3339, *endFlag)
		if err != nil {
			return fmt.Errorf("-end: %w", err)
		}
		end = t
	}

	conn, err := clickhouse.Open(*url)
	if err != nil {
		return err
	}
	defer conn.Close()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := clickhouse.WaitReady(waitCtx, conn, slog.New(slog.DiscardHandler)); err != nil {
		return err
	}

	switch cmd {
	case "schema":
		if err := clickhouse.Migrate(ctx, *url, nil, slog.New(slog.DiscardHandler)); err != nil {
			return err
		}
		return telemetry.ApplyRetention(ctx, conn, 30, slog.New(slog.DiscardHandler))
	case "paced":
		return paced(ctx, conn, out, *seed, env, *rate, *length)
	case "bulk":
		from := end.Add(-time.Duration(*hours * float64(time.Hour)))
		return bulk(ctx, conn, out, *seed, env, *rate, from, end, *workers, *batch)
	case "queries":
		return queries(ctx, conn, out, env, end, *window, *iterations)
	case "graph":
		return graph(ctx, conn, out, end, *minutes)
	case "stats":
		return stats(ctx, conn, out)
	case "drop":
		for _, t := range []string{"service_edges_1m", "span_metrics_1m_mv", "trace_ids_mv", "span_metrics_1m", "trace_ids", "spans", "goose_db_version"} {
			if err := conn.Exec(ctx, "DROP TABLE IF EXISTS "+t+" SYNC"); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
