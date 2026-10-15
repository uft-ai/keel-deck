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
