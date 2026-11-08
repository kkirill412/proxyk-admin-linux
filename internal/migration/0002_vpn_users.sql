-- SQLite is the source of truth for the users managed in Xray.
-- AUTOINCREMENT keeps a deleted user's Xray identity from being reused.
CREATE TABLE vpn_users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT NOT NULL UNIQUE CHECK (uuid <> ''),
    name TEXT NOT NULL CHECK (name <> ''),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1))
);
