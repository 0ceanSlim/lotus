package api

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeAuthToken(t *testing.T) {
	// Lengths 1..3 mod 3 cover every padding case; 0xfb/0xff bytes force
	// '+' and '/' in standard base64 ('-' and '_' in base64url).
	for _, raw := range []string{"{\xfb\xff}", "{\xfb\xff}!", "{\xfb\xff}!?"} {
		encodings := map[string]string{
			"std":        base64.StdEncoding.EncodeToString([]byte(raw)),
			"raw std":    base64.RawStdEncoding.EncodeToString([]byte(raw)),
			"url":        base64.URLEncoding.EncodeToString([]byte(raw)),
			"raw url":    base64.RawURLEncoding.EncodeToString([]byte(raw)),
			"whitespace": " " + base64.RawURLEncoding.EncodeToString([]byte(raw)) + "\n",
		}
		for name, token := range encodings {
			got, err := decodeAuthToken(token)
			if err != nil {
				t.Fatalf("%s %q: %v", name, token, err)
			}
			if string(got) != raw {
				t.Fatalf("%s %q: got %q, want %q", name, token, got, raw)
			}
		}
	}
}

func TestDecodeAuthTokenRejectsGarbage(t *testing.T) {
	for _, token := range []string{"not base64!", "a", strings.Repeat("*", 8)} {
		if _, err := decodeAuthToken(token); err == nil {
			t.Fatalf("%q: expected an error", token)
		}
	}
}
