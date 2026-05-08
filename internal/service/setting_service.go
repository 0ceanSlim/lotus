package service

import (
	"context"

	"github.com/0ceanSlim/lotus/internal/core"
)

type settingService struct {
	maxUploadSizeBytes int
}

func NewSettingService(maxUploadSizeBytes int) (core.SettingService, error) {
	return &settingService{maxUploadSizeBytes}, nil
}

func (s *settingService) ValidateFileSizeMaxBytes(ctx context.Context, sizeBytes int) error {
	if sizeBytes > s.maxUploadSizeBytes {
		return core.ErrFileSizeLimit
	}
	return nil
}
