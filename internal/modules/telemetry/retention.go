// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// DefaultRetentionDays is how long spans are kept unless configured.
const DefaultRetentionDays = 7

// MaxRetentionDays bounds the retention, at about ten years.
const MaxRetentionDays = 3650

// expiring are the tables whose rows expire, by day, with the column the
// day comes from. The migrations create them; their TTL is configuration.
var expiring = []struct{ table, column string }{
	{"spans", "timestamp"},
	{"trace_ids", "start"},
	{"span_metrics_1m", "minute"},
	{"service_edges_1m", "minute"},
}

// ttlDays reads the days of a TTL such as "TTL toDate(timestamp) +
// toIntervalDay(7)", as ClickHouse shows it in system.tables.
var ttlDays = regexp.MustCompile(`TTL toDate\(\w+\) \+ (?:toIntervalDay\((\d+)\)|INTERVAL (\d+) DAY)`)

// ApplyRetention makes spans and what's built from them expire after days.
// Tables already set that way are left alone, so it's cheap at every
// start. Changing it makes ClickHouse recompute when each part expires, in
// the background.
func ApplyRetention(ctx context.Context, conn clickhouse.Conn, days int, logger *slog.Logger) error {
	if days < 1 || days > MaxRetentionDays {
		return fmt.Errorf("span retention of %d days: use 1 to %d", days, MaxRetentionDays)
	}
	for _, t := range expiring {
		var engine string
		if err := conn.QueryRow(ctx, `SELECT engine_full FROM system.tables WHERE database = currentDatabase() AND name = {name:String}`,
			clickhouse.Named("name", t.table)).Scan(&engine); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("ClickHouse has no %s table: run signalbox-server migrate, or start the server with migrations on", t.table)
			}
			return fmt.Errorf("read the retention of %s: %w", t.table, err)
		}
		if current, ok := parseTTLDays(engine); ok && current == days {
			continue
		}
		// The table name and column are constants above, and days a checked
		// number: DDL takes no parameters.
		stmt := fmt.Sprintf("ALTER TABLE %s MODIFY TTL toDate(%s) + INTERVAL %d DAY", t.table, t.column, days)
		if err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("set the retention of %s: %w", t.table, err)
		}
		logger.InfoContext(ctx, "changed span retention", "table", t.table, "days", days)
	}
	return nil
}

func parseTTLDays(engine string) (int, bool) {
	m := ttlDays.FindStringSubmatch(engine)
	if m == nil {
		return 0, false
	}
	s := m[1]
	if s == "" {
		s = m[2]
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
