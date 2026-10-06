package db

import (
	"context"
)

// ShortLink is one row of short_links: a short code that redirects to a blob.
type ShortLink struct {
	Code    string
	Hash    string
	Pubkey  string
	Created int64
}

const insertShortLink = `-- name: InsertShortLink :exec
insert into short_links(code, hash, pubkey, created) values (?,?,?,?)
`

type InsertShortLinkParams struct {
	Code    string
	Hash    string
	Pubkey  string
	Created int64
}

func (q *Queries) InsertShortLink(ctx context.Context, arg InsertShortLinkParams) error {
	_, err := q.db.ExecContext(ctx, insertShortLink, arg.Code, arg.Hash, arg.Pubkey, arg.Created)
	return err
}

const getShortLink = `-- name: GetShortLink :one
select code, hash, pubkey, created from short_links where code = ?
`

func (q *Queries) GetShortLink(ctx context.Context, code string) (ShortLink, error) {
	row := q.db.QueryRowContext(ctx, getShortLink, code)
	var i ShortLink
	err := row.Scan(&i.Code, &i.Hash, &i.Pubkey, &i.Created)
	return i, err
}

const getShortLinkByOwnerHash = `-- name: GetShortLinkByOwnerHash :one
select code, hash, pubkey, created from short_links where pubkey = ? and hash = ? limit 1
`

type GetShortLinkByOwnerHashParams struct {
	Pubkey string
	Hash   string
}

func (q *Queries) GetShortLinkByOwnerHash(ctx context.Context, arg GetShortLinkByOwnerHashParams) (ShortLink, error) {
	row := q.db.QueryRowContext(ctx, getShortLinkByOwnerHash, arg.Pubkey, arg.Hash)
	var i ShortLink
	err := row.Scan(&i.Code, &i.Hash, &i.Pubkey, &i.Created)
	return i, err
}

const listShortLinksByPubkey = `-- name: ListShortLinksByPubkey :many
select code, hash, pubkey, created from short_links where pubkey = ? order by created desc
`

func (q *Queries) ListShortLinksByPubkey(ctx context.Context, pubkey string) ([]ShortLink, error) {
	rows, err := q.db.QueryContext(ctx, listShortLinksByPubkey, pubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ShortLink
	for rows.Next() {
		var i ShortLink
		if err := rows.Scan(&i.Code, &i.Hash, &i.Pubkey, &i.Created); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const deleteShortLink = `-- name: DeleteShortLink :execrows
delete from short_links where code = ? and pubkey = ?
`

type DeleteShortLinkParams struct {
	Code   string
	Pubkey string
}

func (q *Queries) DeleteShortLink(ctx context.Context, arg DeleteShortLinkParams) (int64, error) {
	res, err := q.db.ExecContext(ctx, deleteShortLink, arg.Code, arg.Pubkey)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const deleteShortLinksByHash = `-- name: DeleteShortLinksByHash :exec
delete from short_links where hash = ?
`

func (q *Queries) DeleteShortLinksByHash(ctx context.Context, hash string) error {
	_, err := q.db.ExecContext(ctx, deleteShortLinksByHash, hash)
	return err
}

const getBlobMetaByHash = `-- name: GetBlobMetaByHash :one
select pubkey, hash, type, size, created from blobs where hash = ? limit 1
`

// GetBlobMetaByHash returns a blob row without its bytes.
func (q *Queries) GetBlobMetaByHash(ctx context.Context, hash string) (BlobMeta, error) {
	row := q.db.QueryRowContext(ctx, getBlobMetaByHash, hash)
	var i BlobMeta
	err := row.Scan(&i.Pubkey, &i.Hash, &i.Type, &i.Size, &i.Created)
	return i, err
}
