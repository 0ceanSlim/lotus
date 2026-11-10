package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type NostrUsersResponse struct {
	Names map[string]string `json:"names"`
}

// NostrUsersService fetches the pubkeys in one or more .well-known/nostr.json
// files and hands their union to the access rules as allowed uploaders.
//
// The configured value may hold several URLs separated by commas or
// whitespace. Each URL's last successful result is kept, so a source that is
// down keeps contributing the members it last returned instead of dropping
// them. The URLs can change at runtime from the admin page; an empty value
// clears the member set.
type NostrUsersService struct {
	urls       []string
	perURL     map[string][]string
	log        *zap.Logger
	httpClient *http.Client
	cachedKeys []string
	mu         sync.RWMutex
	refreshMu  sync.Mutex
	updateFunc func([]string)
}

// ParseNostrUsersURLs splits a configured value into distinct URLs.
func ParseNostrUsersURLs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	seen := make(map[string]bool, len(fields))
	urls := make([]string, 0, len(fields))
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			urls = append(urls, f)
		}
	}
	return urls
}

func NewNostrUsersService(url string, log *zap.Logger, updateFunc func([]string)) *NostrUsersService {
	return &NostrUsersService{
		urls:       ParseNostrUsersURLs(url),
		perURL:     make(map[string][]string),
		log:        log,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cachedKeys: make([]string, 0),
		updateFunc: updateFunc,
	}
}

// SetURL switches the sources. Results for URLs no longer configured are
// dropped right away; the new set is fetched in the background. An empty
// value clears the members immediately.
func (s *NostrUsersService) SetURL(url string) {
	urls := ParseNostrUsersURLs(url)
	s.mu.Lock()
	changed := strings.Join(s.urls, " ") != strings.Join(urls, " ")
	s.urls = urls
	keep := make(map[string]bool, len(urls))
	for _, u := range urls {
		keep[u] = true
	}
	for u := range s.perURL {
		if !keep[u] {
			delete(s.perURL, u)
		}
	}
	s.mu.Unlock()
	if !changed {
		return
	}
	s.publishUnion()
	if len(urls) > 0 {
		go s.refresh(context.Background())
	}
}

func (s *NostrUsersService) currentURLs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.urls...)
}

// publishUnion recomputes the member set from every source's last result.
func (s *NostrUsersService) publishUnion() {
	s.mu.Lock()
	set := make(map[string]bool)
	for _, u := range s.urls {
		for _, k := range s.perURL[u] {
			set[k] = true
		}
	}
	pubkeys := make([]string, 0, len(set))
	for k := range set {
		pubkeys = append(pubkeys, k)
	}
	sort.Strings(pubkeys)
	s.cachedKeys = pubkeys
	s.mu.Unlock()
	if s.updateFunc != nil {
		s.updateFunc(pubkeys)
	}
}

// FetchUsers fetches the pubkeys listed in a single nostr.json URL.
func (s *NostrUsersService) FetchUsers(ctx context.Context, url string) ([]string, error) {
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

	s.log.Info("fetched users from nostr.json", zap.String("url", url), zap.Int("count", len(pubkeys)))
	return pubkeys, nil
}

func (s *NostrUsersService) GetCachedKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, len(s.cachedKeys))
	copy(keys, s.cachedKeys)
	return keys
}

// refresh fetches every source. A failed source keeps its previous result.
// It reports whether any source still has no successful result.
func (s *NostrUsersService) refresh(ctx context.Context) (missing bool) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	for _, url := range s.currentURLs() {
		pubkeys, err := s.FetchUsers(ctx, url)
		s.mu.Lock()
		if err != nil {
			_, have := s.perURL[url]
			missing = missing || !have
			s.mu.Unlock()
			s.log.Error("nostr users fetch failed", zap.String("url", url), zap.Error(err))
			continue
		}
		// Only store if the URL is still configured.
		for _, u := range s.urls {
			if u == url {
				s.perURL[url] = pubkeys
				break
			}
		}
		s.mu.Unlock()
	}
	s.publishUnion()
	return missing
}

// StartPeriodicRefresh fetches now and then every interval until ctx ends.
// While some source has never been fetched successfully (e.g. the network was
// not up at boot) it retries every 15 seconds instead of waiting a full
// interval. With no URL configured the fetch is a no-op.
func (s *NostrUsersService) StartPeriodicRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	const retry = 15 * time.Second
	missing := false
	if len(s.currentURLs()) > 0 {
		missing = s.refresh(ctx)
	}
	go func() {
		for {
			wait := interval
			if missing && retry < interval {
				wait = retry
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
				missing = false
				if len(s.currentURLs()) > 0 {
					missing = s.refresh(ctx)
				}
			}
		}
	}()
}
