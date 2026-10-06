package service

import (
	"context"
	"sync/atomic"

	"github.com/0ceanSlim/lotus/internal/core"
)

type settingService struct {
	maxUploadSizeBytes atomic.Int64
}

func NewSettingService(maxUploadSizeBytes int) (core.SettingService, error) {
	s := &settingService{}
	s.Reload(maxUploadSizeBytes)
	return s, nil
}

// Reload sets the per-upload size limit; zero means no limit.
func (s *settingService) Reload(maxUploadSizeBytes int) {
	s.maxUploadSizeBytes.Store(int64(maxUploadSizeBytes))
}

func (s *settingService) ValidateFileSizeMaxBytes(ctx context.Context, sizeBytes int) error {
	limit := s.maxUploadSizeBytes.Load()
	if limit > 0 && int64(sizeBytes) > limit {
		return core.ErrFileSizeLimit
	}
	return nil
}
