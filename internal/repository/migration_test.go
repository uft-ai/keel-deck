package repository

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
	"github.com/oh-my-cpa/oh-my-cpa/migrations"
)

func TestMigration004SanitizesHistoricalData(t *testing.T) {
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	tempDir, err := os.MkdirTemp("", "oh-my-cpa-migration-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test_upgrade.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Apply migrations 001, 002, 003 manually to simulate a v0.3 database
	for _, versionFile := range []string{"001_initial.sql", "002_usage_ingest.sql", "003_ui_preferences.sql"} {
		content, readErr := migrations.Files.ReadFile(versionFile)
		if readErr != nil {
			t.Fatalf("read migration %s: %v", versionFile, readErr)
		}
		if _, execErr := rawDB.Exec(string(content)); execErr != nil {
			t.Fatalf("exec migration %s: %v", versionFile, execErr)
		}
		ver := 1
		if strings.HasPrefix(versionFile, "002") {
			ver = 2
		} else if strings.HasPrefix(versionFile, "003") {
			ver = 3
		}
		if _, execErr := rawDB.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1000)`, ver); execErr != nil {
			t.Fatalf("record migration %d: %v", ver, execErr)
		}
	}

	// Insert cpa_instance
	if _, err := rawDB.Exec(`
		INSERT INTO cpa_instances (id, name, base_url, usage_addr, management_key_ciphertext, management_key_nonce, created_at, updated_at)
		VALUES ('default', 'Default', 'http://127.0.0.1:8317', '127.0.0.1:8317', x'00', x'00', 100, 100)`); err != nil {
		t.Fatal(err)
	}

	// Seed historical data containing sensitive secrets
	sensitiveSecrets := []string{
		"sk-hist-res-account",
		"sk-hist-res-extra-account",
		"sk-hist-inbox-key",
		"sk-hist-inbox-err-token",
		"sk-hist-err-body-secret",
		"sk-hist-err-auth-status",
		"sk-hist-err-quota-reason",
		"sk-hist-usage-group-key",
		"sk-hist-usage-source",
	}

	// 1. discovered_resources:
	// a. with account & extra.account
	if _, err := rawDB.Exec(`
		INSERT INTO discovered_resources (id, instance_id, resource_key, cpa_resource_type, cpa_driver, protocol_driver, protocol_display, details_json, last_seen_at, created_at, updated_at)
		VALUES ('res-1', 'default', 'k1', 'auth-file', 'openai', 'openai', 'OpenAI', ?, 100, 100, 100)`,
		`{"account":"sk-hist-res-account","extra":{"account":"sk-hist-res-extra-account","account_present":"true","account_type":"api_key","other_junk":"strip_me"}}`); err != nil {
		t.Fatal(err)
	}
	// b. with empty details_json
	if _, err := rawDB.Exec(`
		INSERT INTO discovered_resources (id, instance_id, resource_key, cpa_resource_type, cpa_driver, protocol_driver, protocol_display, details_json, last_seen_at, created_at, updated_at)
		VALUES ('res-2', 'default', 'k2', 'codex-api-key', 'codex', 'openai', 'OpenAI', '{}', 100, 100, 100)`); err != nil {
		t.Fatal(err)
	}
	// c. with malformed json
	if _, err := rawDB.Exec(`
		INSERT INTO discovered_resources (id, instance_id, resource_key, cpa_resource_type, cpa_driver, protocol_driver, protocol_display, details_json, last_seen_at, created_at, updated_at)
		VALUES ('res-3', 'default', 'k3', 'codex-api-key', 'codex', 'openai', 'OpenAI', 'invalid-json-text', 100, 100, 100)`); err != nil {
		t.Fatal(err)
	}

	// 2. usage_inboxes:
	if _, err := rawDB.Exec(`
		INSERT INTO usage_inboxes (id, instance_id, source_mode, message_hash, raw_message, status, attempt_count, last_error, popped_at)
		VALUES (1, 'default', 'http_pull', 'hash1', ?, 'pending', 0, ?, 1000)`,
		`{"request_id":"req-1","api_key":"sk-hist-inbox-key","model":"m","tokens":{"total_tokens":5}}`,
		`failed Authorization: Bearer sk-hist-inbox-err-token`); err != nil {
		t.Fatal(err)
	}

	// 3. error_events:
	if _, err := rawDB.Exec(`
		INSERT INTO error_events (id, instance_id, event_key, request_id, provider, model, auth_id, auth_index, status_code, code, body, retryable, auth_status, quota_reason, timestamp_ms, created_at_ms)
		VALUES (1, 'default', 'err-1', 'req-1', 'openai', 'm', 'a1', 'idx1', 400, 'ERR', ?, 0, ?, ?, 1000, 1000)`,
		`rejected with api_key="sk-hist-err-body-secret"`,
		`invalid status: sk-hist-err-auth-status`,
		`quota exhausted: sk-hist-err-quota-reason`); err != nil {
		t.Fatal(err)
	}

	// 4. usage_events:
	if _, err := rawDB.Exec(`
		INSERT INTO usage_events (id, instance_id, event_key, api_group_key, provider, endpoint, auth_type, request_id, timestamp_ms, source, auth_index, failed, generate, latency_ms, total_tokens, created_at_ms)
		VALUES (1, 'default', 'ev-1', 'sk-hist-usage-group-key', 'openai', 'https://user:pass@example.test/v1?token=secret#frag', 'apikey', 'req-1', 1000, 'sk-hist-usage-source', 'idx1', 0, 1, 10, 5, 1000)`); err != nil {
		t.Fatal(err)
	}

	_ = rawDB.Close()

	// Now open the database through Open with WithMigrationBackup and WithCipher
	backupDir := filepath.Join(tempDir, "backups")
	db, err := Open(context.Background(), dbPath, WithMigrationBackup(cipher, backupDir, 3))
	if err != nil {
		t.Fatalf("Open and migrate failed: %v", err)
	}
	defer db.Close()

	// Verify backup was created before migration 004
	backupFiles, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	encCount := 0
	for _, f := range backupFiles {
		if strings.HasSuffix(f.Name(), ".db.enc") {
			encCount++
		}
	}
	if encCount == 0 {
		t.Fatal("expected encrypted migration backup to be created, found none")
	}

	// Verify that NONE of the sensitive secrets exist anywhere in the migrated database
	for _, secret := range sensitiveSecrets {
		// Check discovered_resources
		var count int
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM discovered_resources WHERE details_json LIKE ?`, "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into discovered_resources.details_json", secret)
		}

		// Check usage_inboxes raw_message and last_error
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM usage_inboxes WHERE raw_message LIKE ? OR last_error LIKE ?`, "%"+secret+"%", "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into usage_inboxes", secret)
		}

		// Check error_events body, auth_status, quota_reason
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM error_events WHERE body LIKE ? OR auth_status LIKE ? OR quota_reason LIKE ?`, "%"+secret+"%", "%"+secret+"%", "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into error_events", secret)
		}

		// Check usage_events api_group_key and source
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM usage_events WHERE api_group_key LIKE ? OR source LIKE ?`, "%"+secret+"%", "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into usage_events", secret)
		}
	}

	// Verify specific sanitized values
	var groupKey, groupLabel, source string
	if err := db.SQL.QueryRow(`SELECT api_group_key, api_group_label, source FROM usage_events WHERE id = 1`).Scan(&groupKey, &groupLabel, &source); err != nil {
		t.Fatal(err)
	}
	if groupLabel != "api_key" {
		t.Fatalf("api_group_label = %q, want api_key", groupLabel)
	}
	if !strings.HasPrefix(groupKey, "hmac:") && groupKey != security.RedactedValue {
		t.Fatalf("api_group_key = %q, want hmac prefix or redacted", groupKey)
	}
	if !strings.HasPrefix(source, "hmac:") && source != security.RedactedValue {
		t.Fatalf("source = %q, want hmac prefix or redacted", source)
	}

	// Verify usage inbox ciphertext was populated and can be decrypted
	var ciphertext, nonce []byte
	var rawMsg string
	if err := db.SQL.QueryRow(`SELECT raw_message_ciphertext, raw_message_nonce, raw_message FROM usage_inboxes WHERE id = 1`).Scan(&ciphertext, &nonce, &rawMsg); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || len(nonce) == 0 {
		t.Fatal("expected raw_message_ciphertext and nonce to be populated")
	}
	decrypted, err := cipher.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("failed to decrypt inbox ciphertext: %v", err)
	}
	if !strings.Contains(string(decrypted), "sk-hist-inbox-key") {
		t.Fatalf("decrypted ciphertext does not contain original payload: %s", string(decrypted))
	}
	if strings.Contains(rawMsg, "sk-hist-inbox-key") {
		t.Fatalf("raw_message projection contains secret: %s", rawMsg)
	}
	if !strings.Contains(rawMsg, security.RedactedValue) {
		t.Fatalf("raw_message projection missing redacted marker: %s", rawMsg)
	}

	// Re-opening the database must succeed idempotently
	db2, err := Open(context.Background(), dbPath, WithMigrationBackup(cipher, backupDir, 3))
	if err != nil {
		t.Fatalf("idempotent reopen failed: %v", err)
	}
	_ = db2.Close()
}

func TestBackupRestoreSmokeAndFreeSpaceCheck(t *testing.T) {
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	tempDir, err := os.MkdirTemp("", "oh-my-cpa-backup-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	backupDir := filepath.Join(tempDir, "backups")

	// 1. Test insufficient free space fails closed
	noSpacePolicy := BackupConfig{
		Cipher:       cipher,
		Directory:    backupDir,
		Retention:    3,
		MinFreeBytes: 10 * 1024 * 1024,
		FreeSpace: func(path string) (uint64, error) {
			return 100, nil // only 100 bytes available
		},
	}

	// Initialize file DB with migration 001 first
	initDB, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = initDB.Close()

	// Re-open with mock insufficient space and simulate pending migration
	// (we can test createMigrationBackup directly)
	db, err := Open(context.Background(), dbPath, WithBackupConfig(noSpacePolicy))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = db.createMigrationBackup(context.Background(), 999)
	if err == nil || !strings.Contains(err.Error(), "insufficient free space") {
		t.Fatalf("expected insufficient free space error, got: %v", err)
	}

	// 2. Test successful backup and restore smoke test
	sufficientPolicy := BackupConfig{
		Cipher:    cipher,
		Directory: backupDir,
		Retention: 2,
		FreeSpace: func(path string) (uint64, error) {
			return 1024 * 1024 * 1024, nil
		},
	}
	db.backup = sufficientPolicy
	if err := db.createMigrationBackup(context.Background(), 100); err != nil {
		t.Fatalf("createMigrationBackup failed: %v", err)
	}

	// Check backup file exists and has correct permissions
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	var backupPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db.enc") {
			backupPath = filepath.Join(backupDir, e.Name())
			break
		}
	}
	if backupPath == "" {
		t.Fatal("no .db.enc file found")
	}

	// Check restore smoke passes
	if err := RestoreBackupSmoke(context.Background(), backupPath, cipher); err != nil {
		t.Fatalf("RestoreBackupSmoke failed: %v", err)
	}

	// Check digest mismatch rejection
	shaFile := backupPath + ".sha256"
	if err := os.WriteFile(shaFile, []byte("0000000000000000000000000000000000000000000000000000000000000000  file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err = RestoreBackupSmoke(context.Background(), backupPath, cipher)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got: %v", err)
	}
	if err := os.Remove(shaFile); err != nil {
		t.Fatal(err)
	}
	err = RestoreBackupSmoke(context.Background(), backupPath, cipher)
	if err == nil || !strings.Contains(err.Error(), "read backup digest") {
		t.Fatalf("expected missing digest error, got: %v", err)
	}

	// 3. Test backup retention: create multiple backups and verify oldest pruned
	for i := 101; i <= 104; i++ {
		if err := db.createMigrationBackup(context.Background(), i); err != nil {
			t.Fatalf("create backup %d: %v", i, err)
		}
	}
	allEntries, _ := os.ReadDir(backupDir)
	encFiles := 0
	for _, e := range allEntries {
		if strings.HasSuffix(e.Name(), ".db.enc") {
			encFiles++
		}
	}
	if encFiles > 2 {
		t.Fatalf("expected at most 2 retained backups, got %d", encFiles)
	}
}

// TestUsageReportingColumnsConvergeFromEveryHistory covers the three schemas an upgrade to 041 can
// start from. A fork revision once used version 39 for `tokens_reported`, so a database from that
// revision has recorded 39 without the current 039's `access_provider` and `is_native_key`.
func TestUsageReportingColumnsConvergeFromEveryHistory(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		through int
		// preparing reproduces the history after the migrator stopped at `through`.
		preparing func(t *testing.T, database *DB)
		// verifying, when set, checks the rows recorded before the upgrade.
		verifying func(t *testing.T, database *DB)
		// wantBackups is the number of pre-migration backups the upgrade takes.
		wantBackups int
	}{
		{
			name:        "a fresh database",
			through:     0,
			preparing:   func(t *testing.T, database *DB) {},
			wantBackups: 0,
		},
		{
			name:        "an upstream database at 040",
			through:     40,
			preparing:   func(t *testing.T, database *DB) {},
			wantBackups: 1,
		},
		{
			name:    "a fork database that recorded the earlier 039 without 040",
			through: 38,
			preparing: func(t *testing.T, database *DB) {
				if _, err := database.SQL.Exec(`ALTER TABLE usage_events ADD COLUMN tokens_reported INTEGER`); err != nil {
					t.Fatalf("prepare the fork 039 schema: %v", err)
				}
				if _, err := database.SQL.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (39, 1000)`); err != nil {
					t.Fatalf("record the fork 039: %v", err)
				}
				if _, err := database.SQL.Exec(`INSERT INTO cpa_instances (id, name, base_url, usage_addr, management_key_ciphertext, management_key_nonce, created_at, updated_at)
					VALUES ('default', 'Default', 'http://127.0.0.1:8317', '127.0.0.1:8317', x'00', x'00', 100, 100)`); err != nil {
					t.Fatalf("record the instance that owns the fork usage event: %v", err)
				}
				if _, err := database.SQL.Exec(`INSERT INTO usage_events (instance_id, event_key, api_group_key, timestamp_ms, created_at_ms, tokens_reported)
					VALUES ('default', 'fork-row', 'group', 1000, 1000, 1)`); err != nil {
					t.Fatalf("record a usage event under the fork 039: %v", err)
				}
			},
			verifying: func(t *testing.T, database *DB) {
				var tokensReported sql.NullInt64
				var accessProvider string
				var isNativeKey int
				if err := database.SQL.QueryRow(`SELECT tokens_reported, access_provider, is_native_key FROM usage_events WHERE event_key = 'fork-row'`).
					Scan(&tokensReported, &accessProvider, &isNativeKey); err != nil {
					t.Fatalf("read the usage event recorded before the upgrade: %v", err)
				}
				if !tokensReported.Valid || tokensReported.Int64 != 1 || accessProvider != "" || isNativeKey != 0 {
					t.Fatalf("usage event after the upgrade = tokens_reported %v, access_provider %q, is_native_key %d; want 1, \"\", 0",
						tokensReported, accessProvider, isNativeKey)
				}
			},
			wantBackups: 1,
		},
		{
			name:    "a database that has only some of the reporting columns",
			through: 38,
			preparing: func(t *testing.T, database *DB) {
				if _, err := database.SQL.Exec(`ALTER TABLE usage_events ADD COLUMN access_provider TEXT NOT NULL DEFAULT ''`); err != nil {
					t.Fatalf("prepare a partial schema: %v", err)
				}
				if _, err := database.SQL.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (39, 1000)`); err != nil {
					t.Fatalf("record 039: %v", err)
				}
			},
			wantBackups: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "usage.db")
			backupDir := filepath.Join(t.TempDir(), "backups")
			cipher, err := crypto.New("01234567890123456789012345678901")
			if err != nil {
				t.Fatal(err)
			}
			options := []OpenOption{WithMigrationBackup(cipher, backupDir, 3)}
			if testCase.through > 0 {
				options = append(options, withMigrationsUntil(testCase.through))
			}
			database, err := Open(ctx, path, options...)
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			t.Cleanup(func() { _ = database.Close() })
			testCase.preparing(t, database)
			if got := countMigrationBackups(t, backupDir); got != 0 {
				t.Fatalf("backups before the upgrade = %d, want 0", got)
			}

			database.migrateUntil = 0
			if err := database.Migrate(ctx); err != nil {
				t.Fatalf("migrating from this history failed: %v", err)
			}
			assertUsageReportingColumns(t, database.SQL)
			assertQuotaResetGrantsColumn(t, database.SQL)
			if testCase.verifying != nil {
				testCase.verifying(t, database)
			}
			applied := countAppliedMigrations(t, database.SQL)
			if want := countMigrationFiles(t); applied != want {
				t.Fatalf("applied migrations = %d, want %d", applied, want)
			}
			if got := countMigrationBackups(t, backupDir); got != testCase.wantBackups {
				t.Fatalf("pre-migration backups = %d, want %d", got, testCase.wantBackups)
			}

			// A second start finds nothing pending: no backup, no change to the ledger.
			if err := database.Migrate(ctx); err != nil {
				t.Fatalf("second start failed: %v", err)
			}
			if got := countAppliedMigrations(t, database.SQL); got != applied {
				t.Fatalf("applied migrations after a second start = %d, want %d", got, applied)
			}
			if got := countMigrationBackups(t, backupDir); got != testCase.wantBackups {
				t.Fatalf("pre-migration backups after a second start = %d, want %d", got, testCase.wantBackups)
			}
		})
	}
}

// TestUsageReportingMigrationAddsColumnsOnlyThroughItsHook pins the reason 041 is a hook: the
// earlier fork 039 must not return under that number, and 041 must not add a column in SQL.
func TestUsageReportingMigrationAddsColumnsOnlyThroughItsHook(t *testing.T) {
	if _, err := migrations.Files.ReadFile("039_usage_events_tokens_reported.sql"); err == nil {
		t.Fatal("the fork's earlier 039 is present again; version 39 belongs to the access-provider migration")
	}
	if migrationHook(41) == nil {
		t.Fatal("migration 041 has no hook, so the usage reporting columns would never be added")
	}
	script, err := migrations.Files.ReadFile("041_usage_events_reporting_columns.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(script), "\n") {
		statement := strings.TrimSpace(line)
		if statement == "" || strings.HasPrefix(statement, "--") {
			continue
		}
		if strings.Contains(strings.ToUpper(statement), "ADD COLUMN") {
			t.Fatalf("migration 041 adds a column in SQL (%q): an unconditional ADD COLUMN fails on one of the histories", statement)
		}
	}
}

func assertUsageReportingColumns(t *testing.T, database *sql.DB) {
	t.Helper()
	want := map[string]struct {
		columnType   string
		notNull      bool
		defaultValue sql.NullString
	}{
		"access_provider": {columnType: "TEXT", notNull: true, defaultValue: sql.NullString{String: "''", Valid: true}},
		"is_native_key":   {columnType: "INTEGER", notNull: true, defaultValue: sql.NullString{String: "0", Valid: true}},
		"tokens_reported": {columnType: "INTEGER"},
	}
	rows, err := database.Query(`SELECT name, type, "notnull", dflt_value FROM pragma_table_info('usage_events')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		if err := rows.Scan(&name, &columnType, &notNull, &defaultValue); err != nil {
			t.Fatal(err)
		}
		expected, ok := want[name]
		if !ok {
			continue
		}
		seen[name] = true
		if columnType != expected.columnType || (notNull == 1) != expected.notNull || defaultValue != expected.defaultValue {
			t.Errorf("usage_events.%s = %s notnull=%d default=%v, want %s notnull=%t default=%v",
				name, columnType, notNull, defaultValue, expected.columnType, expected.notNull, expected.defaultValue)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("usage_events.%s is missing", name)
		}
	}
}

func assertQuotaResetGrantsColumn(t *testing.T, database *sql.DB) {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('quota_snapshots') WHERE name = 'reset_grants_json'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("quota_snapshots.reset_grants_json is missing")
	}
}

func countAppliedMigrations(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func countMigrationFiles(t *testing.T) int {
	t.Helper()
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			count++
		}
	}
	return count
}

func countMigrationBackups(t *testing.T, directory string) int {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".db.enc") {
			count++
		}
	}
	return count
}
