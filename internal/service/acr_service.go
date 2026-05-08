package service

import (
	"context"
	"errors"
	"sync"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrMissingRule  = errors.New("internal server error: missing rule")
)

type ACRService struct {
	rules            map[string][]core.ACR
	nostrUserPubkeys map[string]bool
	mu               sync.RWMutex
	log              *zap.Logger
}

func NewACRService(conf *config.Config, log *zap.Logger) (core.ACRStorage, error) {
	rules := make(map[string][]core.ACR)
	for _, rule := range conf.AccessControlRules {
		if _, ok := rules[rule.Resource]; !ok {
			rules[rule.Resource] = make([]core.ACR, 0, 2)
		}
		rules[rule.Resource] = append(rules[rule.Resource], core.ACR{
			Action:   core.ACRAction(rule.Action),
			Pubkey:   rule.Pubkey,
			Resource: core.ACRResource(rule.Resource),
		})
	}

	return &ACRService{
		rules:            rules,
		nostrUserPubkeys: make(map[string]bool),
		log:              log,
	}, nil
}

func (r *ACRService) UpdateNostrUsers(pubkeys []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nostrUserPubkeys = make(map[string]bool, len(pubkeys))
	for _, pubkey := range pubkeys {
		r.nostrUserPubkeys[pubkey] = true
	}

	r.log.Info("updated nostr users", zap.Int("count", len(pubkeys)))
}

func (r *ACRService) Validate(ctx context.Context, pubkey string, resource core.ACRResource) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if resource == core.ResourceUpload {
		if r.nostrUserPubkeys[pubkey] {
			return nil
		}
	}

	rules, ok := r.rules[string(resource)]
	if !ok {
		return errors.New("invalid state: there must be at least one rule for the resource")
	}

	allowed := false
	for _, rule := range rules {
		if rule.Pubkey == "ALL" {
			if rule.Action == core.ACRActionAllow {
				allowed = true
			} else {
				allowed = false
			}
		}

		if rule.Pubkey == pubkey {
			if rule.Action == core.ACRActionAllow {
				allowed = true
			} else {
				allowed = false
			}
			break
		}
	}

	if !allowed {
		return ErrUnauthorized
	}

	return nil
}
