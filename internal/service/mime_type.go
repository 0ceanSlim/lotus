package service

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

type mimeTypeService struct {
	mu       sync.RWMutex
	allowed  map[string]struct{}
	allowAll bool
	queries  *db.Queries
	log      *zap.Logger
}

func NewMimeTypeService(
	ctx context.Context,
	queries *db.Queries,
	conf *config.Config,
	log *zap.Logger,
) (core.MimeTypeService, error) {
	s := &mimeTypeService{queries: queries, log: log}
	if err := s.Reload(ctx, conf.AllowedMimeTypes); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload replaces the allow list. Every named type must exist in the
// server's MIME table, so a typo is rejected rather than silently blocking
// uploads.
func (s *mimeTypeService) Reload(ctx context.Context, types []string) error {
	allowed := make(map[string]struct{})
	allowAll := false

	if len(types) == 1 && types[0] == "*" {
		allowAll = true
	} else {
		for _, mime := range types {
			if mime == "*" {
				allowAll = true
				continue
			}
			if _, err := s.queries.GetMimeType(ctx, mime); err != nil {
				return fmt.Errorf("%s: %w", mime, core.ErrInvalidMimeType)
			}
			allowed[mime] = struct{}{}
		}
	}

	s.mu.Lock()
	s.allowed = allowed
	s.allowAll = allowAll
	s.mu.Unlock()
	return nil
}

func (s *mimeTypeService) Get(ctx context.Context, mimeType string) (*core.MimeType, error) {
	dbMimeType, err := s.queries.GetMimeType(ctx, mimeType)
	return s.dbMimeTypeIntoCore(dbMimeType), err
}

func (s *mimeTypeService) IsAllowed(ctx context.Context, mimeType string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.allowAll {
		return nil
	}
	if _, ok := s.allowed[mimeType]; !ok {
		return core.ErrMimeTypeNotAllowed
	}
	return nil
}

func (s *mimeTypeService) dbMimeTypeIntoCore(m db.MimeType) *core.MimeType {
	return &core.MimeType{
		Extension: m.Extension,
		MimeType:  m.MimeType,
	}
}
