package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/0ceanslim/grain/client/core/tools"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcutil/bech32"
	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
	ErrInvalidSession  = errors.New("invalid session")
)

type sessionService struct {
	db              *sql.DB
	queries         *db.Queries
	log             *zap.Logger
	sessionDuration time.Duration
	cleanupInterval time.Duration
	cleanupStopChan chan bool
}

func NewSessionService(
	database *sql.DB,
	queries *db.Queries,
	log *zap.Logger,
) (core.SessionService, error) {
	service := &sessionService{
		db:              database,
		queries:         queries,
		log:             log,
		sessionDuration: 24 * time.Hour,
		cleanupInterval: 1 * time.Hour,
		cleanupStopChan: make(chan bool),
	}

	go service.startCleanupRoutine()

	return service, nil
}

func (s *sessionService) CreateSession(
	ctx context.Context,
	publicKey string,
	signingMethod string,
	mode string,
) (*core.Session, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	expiresAt := time.Now().Add(s.sessionDuration).Unix()

	session, err := s.queries.CreateSession(ctx, db.CreateSessionParams{
		ID:            sessionID,
		PublicKey:     publicKey,
		SigningMethod: signingMethod,
		Mode:          mode,
		CreatedAt:     now,
		ExpiresAt:     expiresAt,
		LastActive:    now,
	})
	if err != nil {
		s.log.Error("failed to create session", zap.Error(err))
		return nil, err
	}

	npub, err := tools.EncodePubkey(publicKey)
	if err != nil {
		s.log.Warn("failed to encode npub", zap.Error(err))
		npub = publicKey
	}

	return &core.Session{
		ID:            session.ID,
		PublicKey:     session.PublicKey,
		Npub:          npub,
		SigningMethod: session.SigningMethod,
		Mode:          session.Mode,
		CreatedAt:     session.CreatedAt,
		ExpiresAt:     session.ExpiresAt,
		LastActive:    session.LastActive,
	}, nil
}

func (s *sessionService) GetSession(ctx context.Context, sessionID string) (*core.Session, error) {
	session, err := s.queries.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}

	if time.Now().Unix() > session.ExpiresAt {
		return nil, ErrSessionExpired
	}

	npub, err := tools.EncodePubkey(session.PublicKey)
	if err != nil {
		npub = session.PublicKey
	}

	return &core.Session{
		ID:            session.ID,
		PublicKey:     session.PublicKey,
		Npub:          npub,
		SigningMethod: session.SigningMethod,
		Mode:          session.Mode,
		CreatedAt:     session.CreatedAt,
		ExpiresAt:     session.ExpiresAt,
		LastActive:    session.LastActive,
	}, nil
}

func (s *sessionService) ValidateSession(ctx context.Context, sessionID string) (bool, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionExpired) {
			return false, nil
		}
		return false, err
	}

	return session != nil, nil
}

func (s *sessionService) UpdateLastActive(ctx context.Context, sessionID string) error {
	now := time.Now().Unix()
	return s.queries.UpdateSessionLastActive(ctx, db.UpdateSessionLastActiveParams{
		LastActive: now,
		ID:         sessionID,
	})
}

func (s *sessionService) DeleteSession(ctx context.Context, sessionID string) error {
	return s.queries.DeleteSession(ctx, sessionID)
}

func (s *sessionService) CleanupExpiredSessions(ctx context.Context) error {
	now := time.Now().Unix()
	err := s.queries.DeleteExpiredSessions(ctx, now)
	if err != nil {
		s.log.Error("failed to cleanup expired sessions", zap.Error(err))
		return err
	}
	s.log.Info("cleaned up expired sessions")
	return nil
}

func (s *sessionService) GenerateKeyPair() (*core.KeyPair, error) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, err
	}

	sk := hex.EncodeToString(privKey.Serialize())
	pk := hex.EncodeToString(privKey.PubKey().SerializeCompressed()[1:])

	nsec, err := encodePrivateKey(sk)
	if err != nil {
		return nil, err
	}

	npub, err := tools.EncodePubkey(pk)
	if err != nil {
		return nil, err
	}

	return &core.KeyPair{
		Npub:       npub,
		Nsec:       nsec,
		HexPubkey:  pk,
		HexPrivkey: sk,
	}, nil
}

func (s *sessionService) DecodePublicKey(pubkeyStr string) (string, error) {
	if len(pubkeyStr) > 4 && pubkeyStr[:4] == "npub" {
		pk, err := tools.DecodeNpub(pubkeyStr)
		if err != nil {
			return "", err
		}
		return pk, nil
	}

	if len(pubkeyStr) == 64 {
		return strings.ToLower(pubkeyStr), nil
	}

	return "", errors.New("invalid public key format")
}

func (s *sessionService) DecodePrivateKey(privkeyStr string) (string, error) {
	if len(privkeyStr) > 4 && privkeyStr[:4] == "nsec" {
		sk, err := decodePrivateKey(privkeyStr)
		if err != nil {
			return "", err
		}
		return sk, nil
	}

	if len(privkeyStr) == 64 {
		return strings.ToLower(privkeyStr), nil
	}

	return "", errors.New("invalid private key format")
}

func (s *sessionService) GetPublicKeyFromPrivateKey(privkey string) (string, error) {
	sk, err := s.DecodePrivateKey(privkey)
	if err != nil {
		return "", err
	}

	privKeyBytes, err := hex.DecodeString(sk)
	if err != nil {
		return "", err
	}

	privKeyObj, _ := btcec.PrivKeyFromBytes(privKeyBytes)
	pk := hex.EncodeToString(privKeyObj.PubKey().SerializeCompressed()[1:])

	return pk, nil
}

func (s *sessionService) startCleanupRoutine() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := s.CleanupExpiredSessions(ctx)
			if err != nil {
				s.log.Error("cleanup routine failed", zap.Error(err))
			}
			cancel()
		case <-s.cleanupStopChan:
			s.log.Info("stopping session cleanup routine")
			return
		}
	}
}

func (s *sessionService) StopCleanup() {
	close(s.cleanupStopChan)
}

func generateSessionID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func encodePrivateKey(hexPrivKey string) (string, error) {
	decoded, err := hex.DecodeString(hexPrivKey)
	if err != nil {
		return "", err
	}

	encoded, err := bech32.ConvertBits(decoded, 8, 5, true)
	if err != nil {
		return "", err
	}

	return bech32.Encode("nsec", encoded)
}

func decodePrivateKey(nsec string) (string, error) {
	hrp, data, err := bech32.Decode(nsec)
	if err != nil {
		return "", err
	}

	if hrp != "nsec" {
		return "", errors.New("invalid hrp, expected nsec")
	}

	decodedData, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return "", err
	}

	return strings.ToLower(hex.EncodeToString(decodedData)), nil
}
