-- +migrate Up
CREATE TABLE IF NOT EXISTS sessions
(
    id              TEXT PRIMARY KEY,
    public_key      TEXT NOT NULL,
    signing_method  TEXT NOT NULL,  -- 'browser_extension', 'amber', 'encrypted_key'
    mode            TEXT NOT NULL,  -- 'read' or 'write'
    created_at      INT NOT NULL,
    expires_at      INT NOT NULL,
    last_active     INT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_public_key ON sessions(public_key);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

-- +migrate Down
DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP INDEX IF EXISTS idx_sessions_public_key;
DROP TABLE sessions;
