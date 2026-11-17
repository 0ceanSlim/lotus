package service

import (
	"context"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/retention"
)

// retentionSource is the controller's view of the blobs_0x0 table.
type retentionSource struct{ queries *db.Queries }

func (r retentionSource) Commitments(ctx context.Context, now int64) ([]retention.Commitment, error) {
	rows, err := r.queries.ListBlob0x0Commitments(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]retention.Commitment, len(rows))
	for i, row := range rows {
		out[i] = retention.Commitment{Size: row.Size, Expiry: row.Expiry}
	}
	return out, nil
}

func (r retentionSource) RecentSizes(ctx context.Context, since int64) ([]int64, error) {
	return r.queries.ListBlob0x0SizesSince(ctx, since)
}

// retentionParams turns the config block into controller bounds.
func retentionParams(conf *config.Config) retention.Params {
	rc := conf.RetentionControl
	sizeMax := int64(conf.MaxUploadSizeBytes)
	if conf.ZeroXZero.MaxFileSizeBytes > 0 && (sizeMax <= 0 || conf.ZeroXZero.MaxFileSizeBytes < sizeMax) {
		sizeMax = conf.ZeroXZero.MaxFileSizeBytes
	}
	return retention.Params{
		CapacityBytes: rc.CapacityBytes,
		TargetFill:    rc.TargetFill,
		HardFill:      rc.HardFill,
		TTLMin:        rc.TTLMinDuration(),
		TTLMax:        rc.TTLMaxDuration(),
		Alpha:         rc.Alpha,
		SizeRefBytes:  rc.SizeRefBytes,
		SizeMaxBytes:  sizeMax,
		QuoteHold:     rc.QuoteHoldDuration(),
		MaxRisePerDay: rc.MaxRisePerDay,
	}
}
