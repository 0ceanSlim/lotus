package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

// evictionBatch is how many candidates are fetched per pass when making
// room; a pass repeats until enough is freed or nothing is left.
const evictionBatch = 50

// storageGovernor is the part of a blob service that enforces the overall
// storage cap. Both blob services embed one and hand it their own total,
// candidate and delete functions.
type storageGovernor struct {
	policy  atomic.Pointer[core.StoragePolicy]
	evictMu sync.Mutex
	log     *zap.Logger
}

// SetStoragePolicy replaces the cap and the behaviour at it.
func (g *storageGovernor) SetStoragePolicy(p core.StoragePolicy) {
	g.policy.Store(&p)
}

func (g *storageGovernor) storagePolicy() core.StoragePolicy {
	if p := g.policy.Load(); p != nil {
		return *p
	}
	return core.StoragePolicy{}
}

// ensureSpace is EnsureSpace for a table described by total, candidates and
// del. Under the lock so two uploads do not both see room for the last
// megabyte; the window between this check and the insert is accepted.
func (g *storageGovernor) ensureSpace(
	ctx context.Context,
	size int64,
	total func(context.Context) (int64, error),
	candidates func(ctx context.Context, now, limit int64) ([]db.EvictionCandidate, error),
	del func(context.Context, string) error,
) error {
	p := g.storagePolicy()
	if p.MaxTotalBytes <= 0 {
		return nil
	}
	if size > p.MaxTotalBytes {
		return core.ErrServerFull
	}
	g.evictMu.Lock()
	defer g.evictMu.Unlock()

	used, err := total(ctx)
	if err != nil {
		return err
	}
	if used+size <= p.MaxTotalBytes {
		return nil
	}
	if !p.Evict {
		return core.ErrServerFull
	}

	need := used + size - p.MaxTotalBytes
	var freed int64
	for freed < need {
		now := time.Now().Unix()
		batch, err := candidates(ctx, now, evictionBatch)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return core.ErrServerFull
		}
		for _, c := range batch {
			if freed >= need {
				break
			}
			if err := del(ctx, c.Hash); err != nil {
				if errors.Is(err, core.ErrBlobNotFound) {
					continue
				}
				return err
			}
			freed += c.Size
			if g.log != nil {
				g.log.Info("evicted blob to make room",
					zap.String("hash", c.Hash),
					zap.Int64("size", c.Size),
					zap.Float64("age_hours", float64(now-c.Created)/3600))
			}
		}
	}
	return nil
}
