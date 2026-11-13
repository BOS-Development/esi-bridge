CREATE TABLE IF NOT EXISTS accounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS characters (
    id                      INTEGER PRIMARY KEY, -- EVE character ID
    account_id              INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name                    TEXT NOT NULL,
    refresh_token_enc       BLOB NOT NULL,
    access_token_enc        BLOB NOT NULL,
    access_token_expires_at TEXT NOT NULL,
    scopes                  TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    -- When the token pair was last written (login or refresh). Drives the
    -- "tokens last refreshed" age shown on the character detail page.
    tokens_updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS applications (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    api_key_hash TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS grants (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    character_id   INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    scopes         TEXT NOT NULL DEFAULT '',
    approved_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at     TEXT,
    UNIQUE(application_id, character_id)
);

CREATE TABLE IF NOT EXISTS pending_grants (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    application_id    INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    character_id      INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    requested_scopes  TEXT NOT NULL DEFAULT '',
    requested_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(application_id, character_id)
);

-- app_permissions/pending_app_permissions are app-wide, not tied to a single
-- character (e.g. "list every character on the proxy"), so they're modeled
-- separately from grants/pending_grants rather than reusing character_id.
CREATE TABLE IF NOT EXISTS app_permissions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    permission     TEXT NOT NULL,
    approved_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at     TEXT,
    UNIQUE(application_id, permission)
);

CREATE TABLE IF NOT EXISTS pending_app_permissions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    permission     TEXT NOT NULL,
    requested_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(application_id, permission)
);
