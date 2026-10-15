package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"modernc.org/sqlite"
)

var cancellationFunctionID atomic.Uint64

func TestSQLiteCancelledQueryReleasesRowsAndKeepsData(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory=%t", memory), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				entered := make(chan struct{})
				release := make(chan struct{})
				name := fmt.Sprintf("cancel_query_%d", cancellationFunctionID.Add(1))
				// Functions are copied into each new native connection at open time.
				if err := sqlite.RegisterScalarFunction(name, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
					close(entered)
					<-release
					return int64(42), nil
				}); err != nil {
					t.Fatal(err)
				}
				path := "file::memory:?cache=shared"
				if !memory {
					path = filepath.Join(t.TempDir(), "cancel.db")
				}
				database, err := Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = database.Close() })
				conn, err := database.SQL.Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(t.Context(), `CREATE TABLE cancellation_probe(value TEXT NOT NULL); INSERT INTO cancellation_probe VALUES ('before')`); err != nil {
					t.Fatal(err)
				}

				queryDone := make(chan error, 1)
				go func() {
					rows, err := conn.QueryContext(ctx, "SELECT "+name+"(), value FROM cancellation_probe")
					if rows != nil {
						_ = rows.Close()
					}
					queryDone <- err
				}()
				select {
				case <-entered:
					cancel()
					// Let the driver's interrupt goroutine finish before the query can
					// return. Context cancellation alone does not order these goroutines.
					synctest.Wait()
					close(release)
				case err := <-queryDone:
					t.Fatalf("query returned before cancellation: %v", err)
				}
				if err := <-queryDone; !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled query = %v, want context.Canceled", err)
				}
				if memory {
					// Return the interrupted connection before any successful query can
					// reset its state. Pool reuse must preserve the in-memory database.
					if err := conn.Close(); err != nil {
						t.Fatal(err)
					}
					conn, err = database.SQL.Conn(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
				}

				// A leaked native Rows retains a read transaction even though QueryContext
				// returned no rows to its caller. Checkpoint on that same connection must
				// finish, not merely return a result containing a busy flag.
				var busy, frames, checkpointed int
				if err := conn.QueryRowContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &checkpointed); err != nil {
					t.Fatalf("checkpoint after cancellation: %v", err)
				}
				wantFrames := 0
				if memory {
					wantFrames = -1 // In-memory SQLite does not use WAL.
				}
				if busy != 0 || frames != wantFrames || checkpointed != wantFrames {
					t.Fatalf("checkpoint = (%d, %d, %d), want (0, %d, %d)", busy, frames, checkpointed, wantFrames, wantFrames)
				}
				if _, err := conn.ExecContext(t.Context(), `INSERT INTO cancellation_probe VALUES ('after')`); err != nil {
					t.Fatal(err)
				}
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
				var before, after int
				if err := database.SQL.QueryRowContext(t.Context(), `SELECT COUNT(CASE WHEN value = 'before' THEN 1 END), COUNT(CASE WHEN value = 'after' THEN 1 END) FROM cancellation_probe`).Scan(&before, &after); err != nil {
					t.Fatal(err)
				}
				if before != 1 || after != 1 {
					t.Fatalf("stored rows after cancellation = (%d, %d), want (1, 1)", before, after)
				}
			})
		})
	}
}
