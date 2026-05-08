package core

import "context"

type Session struct {
	ID            string
	PublicKey     string
	Npub          string
	SigningMethod string
	Mode          string
	CreatedAt     int64
	ExpiresAt     int64
	LastActive    int64
}

type KeyPair struct {
	Npub       string
	Nsec       string
	HexPubkey  string
	HexPrivkey string
}

type SessionService interface {
	CreateSession(ctx context.Context, publicKey string, signingMethod string, mode string) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	ValidateSession(ctx context.Context, sessionID string) (bool, error)
	UpdateLastActive(ctx context.Context, sessionID string) error
	DeleteSession(ctx context.Context, sessionID string) error
	CleanupExpiredSessions(ctx context.Context) error
	GenerateKeyPair() (*KeyPair, error)
	DecodePublicKey(pubkeyStr string) (string, error)
	DecodePrivateKey(privkeyStr string) (string, error)
	GetPublicKeyFromPrivateKey(privkey string) (string, error)
}
