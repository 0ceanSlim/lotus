// Hand-written in the sqlc style of the other files in this package.
// source: blobs_0x0 token and retention queries

package db

import (
	"context"
	"database/sql"
)

const setBlob0x0Token = `-- name: SetBlob0x0Token :exec
UPDATE blobs_0x0 SET token = ? WHERE hash = ?
`

func (q *Queries) SetBlob0x0Token(ctx context.Context, token string, hash string) error {
	_, err := q.db.ExecContext(ctx, setBlob0x0Token, token, hash)
	return err
}

const getBlob0x0Token = `-- name: GetBlob0x0Token :one
SELECT COALESCE(token, '') FROM blobs_0x0 WHERE hash = ? LIMIT 1
`

func (q *Queries) GetBlob0x0Token(ctx context.Context, hash string) (string, error) {
	var token string
	err := q.db.QueryRowContext(ctx, getBlob0x0Token, hash).Scan(&token)
	return token, err
}

const updateBlob0x0Expiry = `-- name: UpdateBlob0x0Expiry :exec
UPDATE blobs_0x0 SET expiry = ? WHERE hash = ?
`

func (q *Queries) UpdateBlob0x0Expiry(ctx context.Context, expiry sql.NullInt64, hash string) error {
	_, err := q.db.ExecContext(ctx, updateBlob0x0Expiry, expiry, hash)
	return err
}

// Commitment is one stored blob as the retention controller sees it: how
// many bytes, and when they leave.
type Commitment struct {
	Size   int64
	Expiry int64
}

const listBlob0x0Commitments = `-- name: ListBlob0x0Commitments :many
SELECT size, COALESCE(expiry, 0) FROM blobs_0x0 WHERE expiry IS NULL OR expiry > ?
`

func (q *Queries) ListBlob0x0Commitments(ctx context.Context, now int64) ([]Commitment, error) {
	rows, err := q.db.QueryContext(ctx, listBlob0x0Commitments, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Commitment
	for rows.Next() {
		var i Commitment
		if err := rows.Scan(&i.Size, &i.Expiry); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const sumBlob0x0BytesSince = `-- name: SumBlob0x0BytesSince :one
SELECT COALESCE(SUM(size), 0) FROM blobs_0x0 WHERE created >= ?
`

func (q *Queries) SumBlob0x0BytesSince(ctx context.Context, since int64) (int64, error) {
	var total int64
	err := q.db.QueryRowContext(ctx, sumBlob0x0BytesSince, since).Scan(&total)
	return total, err
}

const listBlob0x0SizesSince = `-- name: ListBlob0x0SizesSince :many
SELECT size FROM blobs_0x0 WHERE created >= ?
`

func (q *Queries) ListBlob0x0SizesSince(ctx context.Context, since int64) ([]int64, error) {
	rows, err := q.db.QueryContext(ctx, listBlob0x0SizesSince, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []int64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return items, rows.Err()
}
