-- Store a digest of the random cookie token. Sessions expire and become
-- unusable as soon as the administrator's credential version changes.
CREATE TABLE admin_sessions (
    token_hash BLOB PRIMARY KEY NOT NULL CHECK (length(token_hash) = 32),
    csrf_token TEXT NOT NULL,
    credential_version INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) WITHOUT ROWID;
