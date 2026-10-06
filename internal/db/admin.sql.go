package db

import (
	"context"
	"database/sql"
)

const listPubkeyUsage = `-- name: ListPubkeyUsage :many
select pubkey, count(*) as files, coalesce(sum(size), 0) as bytes, max(created) as last_upload
from blobs
group by pubkey
order by bytes desc
limit 500
`

// PubkeyUsage is one uploader's footprint on this server.
type PubkeyUsage struct {
	Pubkey     string
	Files      int64
	Bytes      int64
	LastUpload sql.NullInt64
}

func (q *Queries) ListPubkeyUsage(ctx context.Context) ([]PubkeyUsage, error) {
	rows, err := q.db.QueryContext(ctx, listPubkeyUsage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PubkeyUsage
	for rows.Next() {
		var i PubkeyUsage
		if err := rows.Scan(&i.Pubkey, &i.Files, &i.Bytes, &i.LastUpload); err != nil {
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
