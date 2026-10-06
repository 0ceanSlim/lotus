package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrMissingRule  = errors.New("internal server error: missing rule")
)

// ACRService evaluates the access rules from config plus the pubkeys fetched
// from the operator's nostr.json, which may upload.
type ACRService struct {
	rules            map[string][]core.ACR
	nostrUserPubkeys map[string]bool
	mu               sync.RWMutex
	log              *zap.Logger
}

func NewACRService(conf *config.Config, log *zap.Logger) (core.ACRStorage, error) {
	s := &ACRService{
		nostrUserPubkeys: make(map[string]bool),
		log:              log,
	}
	s.Reload(conf.AccessControlRules)
	return s, nil
}

// Reload replaces the configured rules. The nostr.json member set is kept.
func (r *ACRService) Reload(configured []config.AccessControlRule) {
	rules := make(map[string][]core.ACR)
	for _, rule := range configured {
		resource := strings.ToUpper(rule.Resource)
		rules[resource] = append(rules[resource], core.ACR{
			Action:   core.ACRAction(strings.ToUpper(rule.Action)),
			Pubkey:   rule.Pubkey,
			Resource: core.ACRResource(resource),
		})
	}
	r.mu.Lock()
	r.rules = rules
	r.mu.Unlock()
}

func (r *ACRService) UpdateNostrUsers(pubkeys []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nostrUserPubkeys = make(map[string]bool, len(pubkeys))
	for _, pubkey := range pubkeys {
		r.nostrUserPubkeys[strings.ToLower(pubkey)] = true
	}

	r.log.Info("updated nostr users", zap.Int("count", len(pubkeys)))
}

// Validate decides whether pubkey may perform resource. A resource with no
// rules at all is denied: a fresh server accepts no uploads until the
// operator claims it or configures access.
func (r *ACRService) Validate(ctx context.Context, pubkey string, resource core.ACRResource) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pubkey = strings.ToLower(pubkey)
	if resource == core.ResourceUpload && r.nostrUserPubkeys[pubkey] {
		return nil
	}

	rules := r.rules[string(resource)]
	if len(rules) == 0 {
		return ErrUnauthorized
	}

	allowed := false
	for _, rule := range rules {
		if rule.Pubkey == "ALL" {
			allowed = rule.Action == core.ACRActionAllow
		}
		if strings.EqualFold(rule.Pubkey, pubkey) {
			allowed = rule.Action == core.ACRActionAllow
			break
		}
	}

	if !allowed {
		return ErrUnauthorized
	}
	return nil
}
