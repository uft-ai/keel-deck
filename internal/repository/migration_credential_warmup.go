package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// Version 41 records either the upstream warmup schema or the fork's reporting
// columns. Reconcile both histories without changing their existing rows.
func reconcileCredentialWarmupTx(ctx context.Context, tx *sql.Tx, db *DB) error {
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
