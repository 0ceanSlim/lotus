package bud01

import (
	"context"

	"github.com/0ceanSlim/lotus/internal/core"
)

func HasBlob(ctx context.Context, services core.Services, hash string) (bool, error) {
	return services.Blob().Exists(ctx, hash)
}
