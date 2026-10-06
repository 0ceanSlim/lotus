package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

// shortCodeAlphabet leaves out characters that read ambiguously (0/o, 1/l/i)
// so a code survives being read aloud or typed from a screenshot.
const shortCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// shortCodeLength gives 31^7, about 27 billion codes.
const shortCodeLength = 7

type shortLinkService struct {
	queries *db.Queries
}

func NewShortLinkService(queries *db.Queries) core.ShortLinkService {
	return &shortLinkService{queries: queries}
}

func (s *shortLinkService) GetOrCreate(ctx context.Context, pubkey, hash string) (*core.ShortLink, error) {
	pubkey = strings.ToLower(pubkey)
	hash = strings.ToLower(hash)
	if existing, err := s.queries.GetShortLinkByOwnerHash(ctx, db.GetShortLinkByOwnerHashParams{Pubkey: pubkey, Hash: hash}); err == nil {
		return fromRow(existing), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// A collision on the primary key is astronomically unlikely but costs
	// nothing to retry.
	for attempt := 0; attempt < 5; attempt++ {
		code, err := generateShortCode()
		if err != nil {
			return nil, err
		}
		link := db.InsertShortLinkParams{Code: code, Hash: hash, Pubkey: pubkey, Created: time.Now().Unix()}
		if err := s.queries.InsertShortLink(ctx, link); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return nil, err
		}
		return &core.ShortLink{Code: code, Hash: hash, Pubkey: pubkey, Created: link.Created}, nil
	}
	return nil, errors.New("could not allocate a short code")
}

func (s *shortLinkService) Resolve(ctx context.Context, code string) (*core.ShortLink, error) {
	row, err := s.queries.GetShortLink(ctx, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, core.ErrShortLinkNotFound
		}
		return nil, err
	}
	return fromRow(row), nil
}

func (s *shortLinkService) ListForPubkey(ctx context.Context, pubkey string) ([]*core.ShortLink, error) {
	rows, err := s.queries.ListShortLinksByPubkey(ctx, strings.ToLower(pubkey))
	if err != nil {
		return nil, err
	}
	out := make([]*core.ShortLink, len(rows))
	for i := range rows {
		out[i] = fromRow(rows[i])
	}
	return out, nil
}

func (s *shortLinkService) Revoke(ctx context.Context, pubkey, code string) error {
	n, err := s.queries.DeleteShortLink(ctx, db.DeleteShortLinkParams{Code: code, Pubkey: strings.ToLower(pubkey)})
	if err != nil {
		return err
	}
	if n == 0 {
		return core.ErrShortLinkNotFound
	}
	return nil
}

func (s *shortLinkService) DeleteForHash(ctx context.Context, hash string) error {
	return s.queries.DeleteShortLinksByHash(ctx, strings.ToLower(hash))
}

func fromRow(r db.ShortLink) *core.ShortLink {
	return &core.ShortLink{Code: r.Code, Hash: r.Hash, Pubkey: r.Pubkey, Created: r.Created}
}

func generateShortCode() (string, error) {
	buf := make([]byte, shortCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random short code: %w", err)
	}
	out := make([]byte, shortCodeLength)
	for i, b := range buf {
		out[i] = shortCodeAlphabet[int(b)%len(shortCodeAlphabet)]
	}
	return string(out), nil
}

// isUniqueViolation recognises SQLite's primary-key conflict without
// importing the driver's error types.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
