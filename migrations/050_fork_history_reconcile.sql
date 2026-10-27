-- Reconciles the fork and upstream histories after both shipped a version 42.
--
-- A fork database recorded 042 as the credential-warmup schema, so upstream's
-- 042_fonts never runs on it. Other databases ran 042_fonts but never received
-- the fork's warmup tables. Both sets of tables are created here only when they
-- are missing; their definitions match upstream 041/042 exactly. The Go hook
-- adds the columns SQLite cannot add conditionally: quota_snapshots.warmup_json
-- and the usage reporting columns, including tokens_reported.

CREATE TABLE IF NOT EXISTS credential_warmup_policies (
 instance_id TEXT NOT NULL REFERENCES cpa_instances(id),
 scope TEXT NOT NULL CHECK(scope IN ('provider','credential')),
 target TEXT NOT NULL,
 document_json TEXT NOT NULL CHECK(json_valid(document_json)),
 updated_at_ms INTEGER NOT NULL,
 PRIMARY KEY(instance_id,scope,target)
);
CREATE TABLE IF NOT EXISTS credential_warmup_state (
 instance_id TEXT NOT NULL REFERENCES cpa_instances(id),
 auth_index TEXT NOT NULL,
 document_json TEXT NOT NULL CHECK(json_valid(document_json)),
 updated_at_ms INTEGER NOT NULL,
 PRIMARY KEY(instance_id,auth_index)
);
CREATE TABLE IF NOT EXISTS credential_warmup_runs (
 instance_id TEXT NOT NULL REFERENCES cpa_instances(id),
 auth_index TEXT NOT NULL,
 request_id TEXT NOT NULL,
 document_json TEXT NOT NULL CHECK(json_valid(document_json)),
 updated_at_ms INTEGER NOT NULL,
 PRIMARY KEY(instance_id,auth_index,request_id)
);

CREATE TABLE IF NOT EXISTS fonts (
    id TEXT PRIMARY KEY,
    family TEXT NOT NULL,
    source TEXT NOT NULL CHECK(source IN ('google','upload')),
    created_at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS font_faces (
    id TEXT PRIMARY KEY,
    font_id TEXT NOT NULL REFERENCES fonts(id) ON DELETE CASCADE,
    weight_min INTEGER NOT NULL CHECK(weight_min BETWEEN 1 AND 1000),
    weight_max INTEGER NOT NULL CHECK(weight_max BETWEEN weight_min AND 1000),
    style TEXT NOT NULL CHECK(style IN ('normal','italic')),
    unicode_range TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL CHECK(format IN ('woff2','woff','truetype','opentype')),
    content BLOB NOT NULL,
    position INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS font_faces_font ON font_faces(font_id, position);
