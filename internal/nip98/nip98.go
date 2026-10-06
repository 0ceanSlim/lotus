// Package nip98 verifies NIP-98 HTTP Auth: a signed kind-27235 event carried
// in the Authorization header ("Nostr <base64 json>") that binds the signer
// to one HTTP method, one absolute URL, and (when a body is sent) one body
// hash. Lotus uses it to bind a login to a proof of key possession and, later,
// to gate admin mutations.
//
// grain ships the same check in server/handlers, but that package drags the
// relay's cgo database in with it, so lotus keeps its own forty lines on top
// of grain's signature verifier.
package nip98

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	nostr "github.com/0ceanslim/grain/server/types"
	"github.com/0ceanslim/grain/server/validation"
)

// Kind is the event kind NIP-98 reserves for HTTP Auth.
const Kind = 27235

// Window is the tolerance applied to created_at in both directions.
const Window = 60 * time.Second

// ErrMissing is returned when no Authorization header is present at all, so
// callers can distinguish "no credentials" from "bad credentials".
var ErrMissing = errors.New("missing Authorization header")

// Extract pulls the signed event out of the Authorization header without
// verifying anything.
func Extract(r *http.Request) (nostr.Event, error) {
	hdr := strings.TrimSpace(r.Header.Get("Authorization"))
	if hdr == "" {
		return nostr.Event{}, ErrMissing
	}
	parts := strings.SplitN(hdr, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "nostr") {
		return nostr.Event{}, errors.New("Authorization header must use the Nostr scheme")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return nostr.Event{}, fmt.Errorf("invalid base64 in Authorization header: %w", err)
	}
	var evt nostr.Event
	if err := json.Unmarshal(raw, &evt); err != nil {
		return nostr.Event{}, fmt.Errorf("invalid event JSON in Authorization header: %w", err)
	}
	return evt, nil
}

// Verify checks the NIP-98 event on r against the request's method, absolute
// URL and body, and returns the authenticated pubkey.
//
// body is the already-read request body (nil or empty when there is none).
// trustedBase, when non-empty, is the public base URL the deployment is
// reachable at (lotus's cdn_url); a u tag built on it is accepted in
// addition to the URL reconstructed from the request and its X-Forwarded-*
// headers, so a server behind a proxy that strips those headers still works.
func Verify(r *http.Request, body []byte, trustedBase string) (string, error) {
	evt, err := Extract(r)
	if err != nil {
		return "", err
	}
	return VerifyEvent(evt, r, body, trustedBase)
}

// VerifyEvent is Verify for an already-extracted event.
func VerifyEvent(evt nostr.Event, r *http.Request, body []byte, trustedBase string) (string, error) {
	if evt.Kind != Kind {
		return "", fmt.Errorf("expected kind %d, got %d", Kind, evt.Kind)
	}
	if !validation.CheckSignature(evt) {
		return "", errors.New("invalid event signature")
	}
	now := time.Now()
	created := time.Unix(evt.CreatedAt, 0)
	if created.Before(now.Add(-Window)) || created.After(now.Add(Window)) {
		return "", errors.New("event created_at is outside the allowed window")
	}

	var uTag, methodTag, payloadTag string
	for _, t := range evt.Tags {
		if len(t) < 2 {
			continue
		}
		switch t[0] {
		case "u":
			uTag = t[1]
		case "method":
			methodTag = t[1]
		case "payload":
			payloadTag = t[1]
		}
	}
	if uTag == "" || methodTag == "" {
		return "", errors.New("event is missing the u or method tag")
	}
	if !strings.EqualFold(methodTag, r.Method) {
		return "", fmt.Errorf("method tag %q does not match request method %s", methodTag, r.Method)
	}
	if !urlMatches(uTag, candidateURLs(r, trustedBase)) {
		return "", fmt.Errorf("u tag %q does not match the request URL", uTag)
	}

	if len(body) > 0 {
		sum := sha256.Sum256(body)
		want := hex.EncodeToString(sum[:])
		if !strings.EqualFold(payloadTag, want) {
			return "", errors.New("payload tag does not match the request body hash")
		}
	} else if payloadTag != "" {
		empty := sha256.Sum256(nil)
		if !strings.EqualFold(payloadTag, hex.EncodeToString(empty[:])) {
			return "", errors.New("payload tag present on a request without a body")
		}
	}
	return strings.ToLower(evt.PubKey), nil
}

// candidateURLs lists the absolute URLs this request may legitimately have
// been signed for: the one reconstructed from the request (honoring reverse
// proxy hints) and, when configured, the one built on the trusted base.
func candidateURLs(r *http.Request, trustedBase string) []string {
	out := []string{RequestURL(r)}
	if trustedBase != "" {
		base := strings.TrimRight(trustedBase, "/")
		out = append(out, base+r.URL.RequestURI())
	}
	return out
}

func urlMatches(tag string, candidates []string) bool {
	tu, err := url.Parse(tag)
	if err != nil {
		return false
	}
	for _, c := range candidates {
		cu, err := url.Parse(c)
		if err != nil {
			continue
		}
		if strings.EqualFold(tu.Scheme, cu.Scheme) &&
			strings.EqualFold(tu.Host, cu.Host) &&
			tu.EscapedPath() == cu.EscapedPath() &&
			tu.RawQuery == cu.RawQuery {
			return true
		}
	}
	return false
}

// RequestURL reconstructs the absolute URL the client used, honoring the
// standard reverse-proxy headers so a server behind nginx or traefik sees
// the public https URL rather than the proxied http://127.0.0.1 one.
func RequestURL(r *http.Request) string {
	scheme := "http"
	if proto := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(proto)
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := firstHeaderValue(r.Header.Get("X-Forwarded-Host")); h != "" {
		host = h
	}
	return scheme + "://" + host + r.URL.RequestURI()
}

func firstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// Encode produces the Authorization header value for a signed event.
func Encode(evt nostr.Event) (string, error) {
	raw, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}
	return "Nostr " + base64.StdEncoding.EncodeToString(raw), nil
}
