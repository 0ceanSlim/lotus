// Hand-written in the sqlc style (there is no sqlc config): the queries
// behind the overall storage cap. Both blob tables get the same pair.

package db

import "context"

const getTotalBlobBytes = `-- name: GetTotalBlobBytes :one
select COALESCE(SUM(size), 0) as total_size
from blobs
`

// GetTotalBlobBytes is the bytes stored in the blobs table.
func (q *Queries) GetTotalBlobBytes(ctx context.Context) (int64, error) {
	row := q.db.QueryRowContext(ctx, getTotalBlobBytes)
	var totalSize int64
	err := row.Scan(&totalSize)
	return totalSize, err
}

const getTotalBlob0x0Bytes = `-- name: GetTotalBlob0x0Bytes :one
select COALESCE(SUM(size), 0) as total_size
from blobs_0x0
`

// GetTotalBlob0x0Bytes is the bytes recorded in the blobs_0x0 table.
func (q *Queries) GetTotalBlob0x0Bytes(ctx context.Context) (int64, error) {
	row := q.db.QueryRowContext(ctx, getTotalBlob0x0Bytes)
	var totalSize int64
	err := row.Scan(&totalSize)
	return totalSize, err
}

// EvictionCandidate is a blob ranked for eviction.
type EvictionCandidate struct {
	Hash    string
	Size    int64
	Created int64
}

// EvictionAgeFloor is the age, in seconds, below which blobs are treated as
// equally fresh when ranked for eviction.
const EvictionAgeFloor = 3600

// Eviction order is age × size, largest first, with age floored at an hour:
// an old big file goes before an old small one or a fresh big one, and among
// files uploaded within the last hour the biggest goes first. Without the
// floor a file uploaded seconds ago scores near zero and the loop deletes
// every tiny old blob, freeing nothing, before it reaches the one that
// would actually make room.
const listEvictionCandidates = `-- name: ListEvictionCandidates :many
select hash, size, created
from blobs
order by max(? - created, ?) * size desc
limit ?
`

func (q *Queries) ListEvictionCandidates(ctx context.Context, now int64, limit int64) ([]EvictionCandidate, error) {
	rows, err := q.db.QueryContext(ctx, listEvictionCandidates, now, EvictionAgeFloor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvictionCandidates(rows)
}

const listEviction0x0Candidates = `-- name: ListEviction0x0Candidates :many
select hash, size, created
from blobs_0x0
order by max(? - created, ?) * size desc
limit ?
`

func (q *Queries) ListEviction0x0Candidates(ctx context.Context, now int64, limit int64) ([]EvictionCandidate, error) {
	rows, err := q.db.QueryContext(ctx, listEviction0x0Candidates, now, EvictionAgeFloor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvictionCandidates(rows)
}

func scanEvictionCandidates(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]EvictionCandidate, error) {
	var items []EvictionCandidate
	for rows.Next() {
		var i EvictionCandidate
		if err := rows.Scan(&i.Hash, &i.Size, &i.Created); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
