package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

type NostrUsersResponse struct {
	Names map[string]string `json:"names"`
}

type NostrUsersService struct {
	url        string
	log        *zap.Logger
	httpClient *http.Client
	cachedKeys []string
	mu         sync.RWMutex
	updateFunc func([]string)
}

func NewNostrUsersService(url string, log *zap.Logger, updateFunc func([]string)) *NostrUsersService {
	return &NostrUsersService{
		url:        url,
		log:        log,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cachedKeys: make([]string, 0),
		updateFunc: updateFunc,
	}
}

func (s *NostrUsersService) FetchUsers(ctx context.Context) ([]string, error) {
	if s.url == "" {
		s.log.Info("nostr_users_url is empty, skipping automatic user fetching")
		return []string{}, nil
	}

	s.log.Info("fetching users from nostr.json", zap.String("url", s.url))

	req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching nostr.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var nostrResp NostrUsersResponse
	if err := json.Unmarshal(body, &nostrResp); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	pubkeys := make([]string, 0, len(nostrResp.Names))
	for _, pubkey := range nostrResp.Names {
		pubkeys = append(pubkeys, pubkey)
	}

	s.log.Info("fetched users from nostr.json", zap.Int("count", len(pubkeys)))

	s.mu.Lock()
	s.cachedKeys = pubkeys
	s.mu.Unlock()

	return pubkeys, nil
}

func (s *NostrUsersService) GetCachedKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, len(s.cachedKeys))
	copy(keys, s.cachedKeys)
	return keys
}

func (s *NostrUsersService) StartPeriodicRefresh(ctx context.Context, interval time.Duration) {
	pubkeys, err := s.FetchUsers(ctx)
	if err != nil {
		s.log.Error("initial fetch failed", zap.Error(err))
	} else if s.updateFunc != nil {
		s.updateFunc(pubkeys)
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.log.Info("stopping nostr users periodic refresh")
				return
			case <-ticker.C:
				pubkeys, err := s.FetchUsers(ctx)
				if err != nil {
					s.log.Error("periodic fetch failed", zap.Error(err))
					continue
				}

				if s.updateFunc != nil {
					s.updateFunc(pubkeys)
				}
			}
		}
	}()
}
