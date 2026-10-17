// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// stats reports how much the tables hold and how big they are on disk,
// from system.parts and system.columns.
func stats(ctx context.Context, conn clickhouse.Conn, out io.Writer) error {
	rows, err := conn.Query(ctx, `
		SELECT table, count() AS parts, sum(rows), sum(data_compressed_bytes), sum(data_uncompressed_bytes),
		       sum(primary_key_bytes_in_memory), sum(secondary_indices_compressed_bytes), sum(bytes_on_disk)
		FROM system.parts
		WHERE active AND database = currentDatabase()
		GROUP BY table
		ORDER BY sum(bytes_on_disk) DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "table\tparts\trows\tcompressed MB\tuncompressed MB\tratio\tprimary key MB (RAM)\tskip indexes MB\ton disk MB\tbytes per row\t")
	var spanRows, totalDisk uint64
	for rows.Next() {
		var (
			table                                              string
			parts, n, compressed, uncompressed, pk, skip, disk uint64
		)
		if err := rows.Scan(&table, &parts, &n, &compressed, &uncompressed, &pk, &skip, &disk); err != nil {
			return err
		}
		if table == "spans" {
			spanRows = n
		}
		totalDisk += disk
		ratio, perRow := 0.0, 0.0
		if compressed > 0 {
			ratio = float64(uncompressed) / float64(compressed)
		}
		if n > 0 {
			perRow = float64(disk) / float64(n)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.1f\t%.1f\t%.1f\t%.2f\t%.1f\t%.1f\t%.1f\t\n", table, parts, n,
			mb(compressed), mb(uncompressed), ratio, mb(pk), mb(skip), mb(disk), perRow)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if spanRows > 0 {
		fmt.Fprintf(out, "\nall tables: %.1f MB on disk, %.1f bytes per span, %.1f GB per day at 5,000 spans/s\n",
			mb(totalDisk), float64(totalDisk)/float64(spanRows), float64(totalDisk)/float64(spanRows)*5000*86400/1e9)
	}

	cols, err := conn.Query(ctx, `
		SELECT name, data_compressed_bytes, data_uncompressed_bytes
		FROM system.columns
		WHERE database = currentDatabase() AND table = 'spans' AND data_compressed_bytes > 0
		ORDER BY data_compressed_bytes DESC
		LIMIT 14`)
	if err != nil {
		return err
	}
	defer cols.Close()
	fmt.Fprintln(out, "\nlargest span columns:")
	tw = tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "column\tcompressed MB\tratio\tbytes per span\t")
	for cols.Next() {
		var (
			name                     string
			compressed, uncompressed uint64
		)
		if err := cols.Scan(&name, &compressed, &uncompressed); err != nil {
			return err
		}
		perSpan := 0.0
		if spanRows > 0 {
			perSpan = float64(compressed) / float64(spanRows)
		}
		fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t\n", name, mb(compressed), float64(uncompressed)/float64(max(compressed, 1)), perSpan)
	}
	if err := cols.Err(); err != nil {
		return err
	}
	return tw.Flush()
}

func mb(b uint64) float64 { return float64(b) / 1e6 }
