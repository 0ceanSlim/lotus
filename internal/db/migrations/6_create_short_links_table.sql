-- +migrate Up
CREATE TABLE IF NOT EXISTS short_links
(
    code    TEXT PRIMARY KEY,
    hash    TEXT NOT NULL,
    pubkey  TEXT NOT NULL,
    created INT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_short_links_pubkey ON short_links(pubkey);
CREATE INDEX IF NOT EXISTS idx_short_links_hash ON short_links(hash);

-- +migrate Down
DROP INDEX IF EXISTS idx_short_links_hash;
DROP INDEX IF EXISTS idx_short_links_pubkey;
DROP TABLE IF EXISTS short_links;
