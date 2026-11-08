-- Existing users retain their UUID and access. Their original creation time was
-- not recorded by v0.2.0, so migration time is used for those rows.
ALTER TABLE vpn_users ADD COLUMN traffic_limit_bytes INTEGER CHECK (traffic_limit_bytes IS NULL OR traffic_limit_bytes >= 0);
ALTER TABLE vpn_users ADD COLUMN uplink_bytes INTEGER NOT NULL DEFAULT 0 CHECK (typeof(uplink_bytes) = 'integer' AND uplink_bytes >= 0);
ALTER TABLE vpn_users ADD COLUMN downlink_bytes INTEGER NOT NULL DEFAULT 0 CHECK (typeof(downlink_bytes) = 'integer' AND downlink_bytes >= 0);
ALTER TABLE vpn_users ADD COLUMN blocked_reason TEXT NOT NULL DEFAULT '' CHECK (blocked_reason IN ('', 'manual', 'limit'));
ALTER TABLE vpn_users ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE vpn_users ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE vpn_users ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE vpn_users ADD COLUMN synced_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vpn_users ADD COLUMN delete_pending INTEGER NOT NULL DEFAULT 0 CHECK (delete_pending IN (0, 1));
UPDATE vpn_users SET created_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    blocked_reason = CASE WHEN enabled = 0 THEN 'manual' ELSE '' END;

-- One collector, one durable pending batch. Replaying that batch is idempotent.
CREATE TABLE traffic_collection (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_batch_id TEXT NOT NULL DEFAULT '',
    sampled_at TEXT
);
INSERT INTO traffic_collection(id) VALUES (1);
