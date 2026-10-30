// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

func TestMigrateCommandMigratesBothDatabases(t *testing.T) {
	pg, pgURL := postgrestest.NewWithURL(t)
	ch, chURL := clickhousetest.NewWithURL(t)
	ctx := context.Background()

	run := func(args ...string) error {
		cmd := newMigrateCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"--log-format", "text"}, args...))
		return cmd.ExecuteContext(ctx)
	}
	if err := run("--database-url", pgURL, "--clickhouse-url", ""); err == nil || !strings.Contains(err.Error(), "SIGNALBOX_CLICKHOUSE_URL") {
		t.Errorf("without ClickHouse: %v", err)
	}
	if err := run("--database-url", pgURL, "--clickhouse-url", chURL, "--database-wait", "30s"); err != nil {
		t.Fatal(err)
	}

	var users bool
	if err := pg.QueryRow(ctx, `SELECT to_regclass('users') IS NOT NULL`).Scan(&users); err != nil || !users {
		t.Errorf("PostgreSQL wasn't migrated: %v", err)
	}
	var spans uint64
	if err := ch.QueryRow(ctx, `SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = 'spans'`).Scan(&spans); err != nil || spans != 1 {
		t.Errorf("ClickHouse wasn't migrated: %d, %v", spans, err)
	}
}
