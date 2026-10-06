package nip98

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0ceanslim/grain/client/core"
	nostr "github.com/0ceanslim/grain/server/types"
)

// signedHeader signs a NIP-98 event for method+target with a fresh key and
// returns the Authorization header value plus the signer's pubkey.
func signedHeader(t *testing.T, method, target string, body []byte, mutate func(*nostr.Event)) (string, string) {
	t.Helper()
	signer, err := core.NewEventSignerFromRandom()
	if err != nil {
		t.Fatal(err)
	}
	tags := [][]string{{"u", target}, {"method", method}}
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		tags = append(tags, []string{"payload", hex.EncodeToString(sum[:])})
	}
	evt := nostr.Event{
		PubKey:    signer.PublicKey(),
		CreatedAt: time.Now().Unix(),
		Kind:      Kind,
		Tags:      tags,
		Content:   "",
	}
	if mutate != nil {
		mutate(&evt)
	}
	if err := signer.SignEvent(&evt); err != nil {
		t.Fatal(err)
	}
	hdr, err := Encode(evt)
	if err != nil {
		t.Fatal(err)
	}
	return hdr, signer.PublicKey()
}

func TestVerifyAcceptsValidLogin(t *testing.T) {
	body := []byte(`{"public_key":"abc","requested_mode":"write"}`)
	target := "http://localhost:8484/api/v1/auth/login"
	hdr, pk := signedHeader(t, "POST", target, body, nil)
	req := httptest.NewRequest("POST", target, bytes.NewReader(body))
	req.Header.Set("Authorization", hdr)
	got, err := Verify(req, body, "")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != pk {
		t.Fatalf("pubkey mismatch: %s vs %s", got, pk)
	}
}

func TestVerifyAcceptsTrustedBaseBehindProxy(t *testing.T) {
	body := []byte(`{}`)
	hdr, _ := signedHeader(t, "POST", "https://blossom.example.com/api/v1/auth/login", body, nil)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8484/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Authorization", hdr)
	if _, err := Verify(req, body, ""); err == nil {
		t.Fatal("expected URL mismatch without a trusted base")
	}
	if _, err := Verify(req, body, "https://blossom.example.com/"); err != nil {
		t.Fatalf("expected trusted base to match, got %v", err)
	}
}

func TestVerifyHonorsForwardedHeaders(t *testing.T) {
	hdr, _ := signedHeader(t, "GET", "https://blossom.example.com/api/v1/admin/x?y=1", nil, nil)
	req := httptest.NewRequest("GET", "http://127.0.0.1:8484/api/v1/admin/x?y=1", nil)
	req.Header.Set("Authorization", hdr)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "blossom.example.com")
	if _, err := Verify(req, nil, ""); err != nil {
		t.Fatalf("expected forwarded headers to match, got %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	body := []byte(`{"a":1}`)
	target := "http://localhost:8484/api/v1/auth/login"
	cases := map[string]struct {
		mutate func(*nostr.Event)
		header bool
	}{
		"missing header":   {header: false},
		"wrong method":     {mutate: func(e *nostr.Event) { e.Tags[1][1] = "GET" }, header: true},
		"wrong url":        {mutate: func(e *nostr.Event) { e.Tags[0][1] = "http://evil.example/api/v1/auth/login" }, header: true},
		"expired":          {mutate: func(e *nostr.Event) { e.CreatedAt = time.Now().Add(-2 * time.Minute).Unix() }, header: true},
		"wrong kind":       {mutate: func(e *nostr.Event) { e.Kind = 24242 }, header: true},
		"tampered payload": {mutate: func(e *nostr.Event) { e.Tags[2][1] = "00" }, header: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hdr, _ := signedHeader(t, "POST", target, body, tc.mutate)
			req := httptest.NewRequest("POST", target, bytes.NewReader(body))
			if tc.header {
				req.Header.Set("Authorization", hdr)
			}
			if _, err := Verify(req, body, ""); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
}

func TestVerifyRejectsBodySwap(t *testing.T) {
	body := []byte(`{"public_key":"abc"}`)
	target := "http://localhost:8484/api/v1/auth/login"
	hdr, _ := signedHeader(t, "POST", target, body, nil)
	swapped := []byte(`{"public_key":"def"}`)
	req := httptest.NewRequest("POST", target, bytes.NewReader(swapped))
	req.Header.Set("Authorization", hdr)
	if _, err := Verify(req, swapped, ""); err == nil {
		t.Fatal("expected rejection when the body differs from the signed payload")
	}
}
