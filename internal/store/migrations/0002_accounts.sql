-- 0002: accounts, sessions and profile fields.
--
-- SQLite cannot add a column that carries a UNIQUE constraint, so email
-- uniqueness is a partial unique index instead: it also lets the bootstrapped
-- admin and judge rows keep a NULL email.

ALTER TABLE users ADD COLUMN email TEXT;
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE users ADD COLUMN last_login_at INTEGER;

-- COLLATE NOCASE inside the index expression, because the column added above
-- defaults to binary collation: without it, "Alice@example.com" and
-- "alice@example.com" would both register.
CREATE UNIQUE INDEX idx_users_email ON users(email COLLATE NOCASE) WHERE email IS NOT NULL;

CREATE TABLE sessions (
    id           TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    ip           TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

-- Failed logins per key (remote address). Counted in the database so a restart
-- cannot be used to reset a lockout.
CREATE TABLE login_attempts (
    key          TEXT    PRIMARY KEY,
    failures     INTEGER NOT NULL DEFAULT 0,
    window_start INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
