package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// Upstream 046 rebuilds usage_events from an explicit column list that predates
// the fork's tokens_reported column. The pre-hook keeps the recorded values in a
// connection-local table inside the migration transaction; the hook restores them.
func preserveTokensReportedTx(ctx context.Context, tx *sql.Tx, _ *DB) error {
	exists, err := tableHasColumn(ctx, tx, "usage_events", "tokens_reported")
	if err != nil || !exists {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE fork_tokens_reported AS
		SELECT id, tokens_reported FROM usage_events WHERE tokens_reported IS NOT NULL`); err != nil {
		return fmt.Errorf("keep usage_events.tokens_reported: %w", err)
	}
	return nil
}

func restoreTokensReportedTx(ctx context.Context, tx *sql.Tx, db *DB) error {
	if err := addUsageReportingColumnsTx(ctx, tx, db); err != nil {
		return err
	}
	var kept int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_temp_master WHERE type = 'table' AND name = 'fork_tokens_reported'`).Scan(&kept); err != nil || kept == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE usage_events SET tokens_reported =
		(SELECT kept.tokens_reported FROM fork_tokens_reported kept WHERE kept.id = usage_events.id)
		WHERE id IN (SELECT id FROM fork_tokens_reported)`); err != nil {
		return fmt.Errorf("restore usage_events.tokens_reported: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE temp.fork_tokens_reported`); err != nil {
		return fmt.Errorf("drop kept tokens_reported: %w", err)
	}
	return nil
}

// Version 50 completes whichever version-42 history the database recorded; see
// 050_fork_history_reconcile.sql. The SQL creates missing tables; this adds columns.
func reconcileForkHistoryTx(ctx context.Context, tx *sql.Tx, db *DB) error {
	if err := addUsageReportingColumnsTx(ctx, tx, db); err != nil {
		return err
	}
	exists, err := tableHasColumn(ctx, tx, "quota_snapshots", "warmup_json")
	if err != nil || exists {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE quota_snapshots ADD COLUMN warmup_json TEXT`); err != nil {
		return fmt.Errorf("add quota_snapshots.warmup_json: %w", err)
	}
	return nil
}
