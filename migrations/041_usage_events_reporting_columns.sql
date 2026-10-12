-- Ensures the usage-event reporting columns exist on every database history.
--
-- `tokens_reported` records whether the provider reported token counters. NULL is a legacy
-- record that omitted the field, 0 means the provider explicitly reported no usable counters,
-- and 1 means the counters were reported, including genuine zeros. Existing integer counters are
-- unchanged. `access_provider` and `is_native_key` are the columns migration 039 defines.
--
-- Why a Go hook adds them rather than an `ALTER TABLE` here: an earlier fork revision shipped its
-- own migration 039 that added `tokens_reported`, so a database from that revision has recorded
-- version 39 without `access_provider` or `is_native_key`, and the current 039 will never run on
-- it. A fresh or upstream database already has those two columns and lacks `tokens_reported`.
-- SQLite has no `ADD COLUMN IF NOT EXISTS`, and a failed statement aborts the migration
-- transaction, so the hook asks the schema which columns exist and adds only the missing ones.

SELECT 1 FROM usage_events LIMIT 0;
