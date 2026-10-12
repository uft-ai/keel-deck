package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// usageReportingColumns are the usage-event columns migration 041 guarantees, in the order they
// are added. The definitions match migration 039 and the original token-reporting migration.
var usageReportingColumns = []struct {
	name       string
	definition string
}{
	{name: "access_provider", definition: `TEXT NOT NULL DEFAULT ''`},
	{name: "is_native_key", definition: `INTEGER NOT NULL DEFAULT 0`},
	{name: "tokens_reported", definition: `INTEGER`},
}

// addUsageReportingColumnsTx adds each usage reporting column that the database does not have.
//
// `041_usage_events_reporting_columns.sql` explains the two histories: a database that recorded
// the fork's earlier migration 039 has `tokens_reported` but skipped the current 039's columns,
// while every other database has those columns and lacks `tokens_reported`.
func addUsageReportingColumnsTx(ctx context.Context, tx *sql.Tx, db *DB) error {
	if tx == nil {
		return errors.New("migration transaction is required")
	}
	for _, column := range usageReportingColumns {
		exists, err := tableHasColumn(ctx, tx, "usage_events", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE usage_events ADD COLUMN %s %s`, column.name, column.definition)); err != nil {
			return fmt.Errorf("add usage_events.%s: %w", column.name, err)
		}
	}
	return nil
}
