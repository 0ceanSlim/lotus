-- +migrate Up
-- The 0x0 backend hands out a management token (X-Token) with each new
-- file; it is the only way to delete the file or change its expiry there.
ALTER TABLE blobs_0x0 ADD COLUMN token TEXT;

-- +migrate Down
ALTER TABLE blobs_0x0 DROP COLUMN token;
