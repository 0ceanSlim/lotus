-- +migrate Up
CREATE TABLE IF NOT EXISTS blobs_0x0 (
    hash    TEXT PRIMARY KEY,
    pubkey  TEXT NOT NULL,
    url     TEXT NOT NULL,
    size    INTEGER NOT NULL,
    type    TEXT NOT NULL,
    expiry  INTEGER,
    created INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blobs_0x0_pubkey ON blobs_0x0(pubkey);
CREATE INDEX IF NOT EXISTS idx_blobs_0x0_expiry ON blobs_0x0(expiry);

-- +migrate Down
DROP TABLE IF EXISTS blobs_0x0;
