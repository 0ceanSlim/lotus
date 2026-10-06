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

// NostrUsersService fetches the pubkeys in a .well-known/nostr.json and hands
// them to the access rules as allowed uploaders. The URL can change at
// runtime from the admin page; an empty URL clears the member set.
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

// SetURL switches the source. A changed URL is fetched right away in the
// background; an empty one clears the members immediately.
func (s *NostrUsersService) SetURL(url string) {
	s.mu.Lock()
	changed := s.url != url
	s.url = url
	s.mu.Unlock()
	if !changed {
		return
	}
	if url == "" {
		s.publish([]string{})
		return
	}
	go s.refresh(context.Background())
}

func (s *NostrUsersService) currentURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.url
}

func (s *NostrUsersService) publish(pubkeys []string) {
	s.mu.Lock()
	s.cachedKeys = pubkeys
	s.mu.Unlock()
	if s.updateFunc != nil {
		s.updateFunc(pubkeys)
	}
}

func (s *NostrUsersService) FetchUsers(ctx context.Context) ([]string, error) {
	url := s.currentURL()
	if url == "" {
		return []string{}, nil
	}

	s.log.Debug("fetching users from nostr.json", zap.String("url", url))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
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
	return pubkeys, nil
}

func (s *NostrUsersService) GetCachedKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, len(s.cachedKeys))
	copy(keys, s.cachedKeys)
	return keys
}

func (s *NostrUsersService) refresh(ctx context.Context) {
	pubkeys, err := s.FetchUsers(ctx)
	if err != nil {
		s.log.Error("nostr users fetch failed", zap.Error(err))
		return
	}
	s.publish(pubkeys)
}

// StartPeriodicRefresh fetches now and then every interval until ctx ends.
// With no URL configured the fetch is a no-op, so it is safe to always run.
func (s *NostrUsersService) StartPeriodicRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if s.currentURL() != "" {
		s.refresh(ctx)
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.currentURL() != "" {
					s.refresh(ctx)
				}
			}
		}
	}()
}
