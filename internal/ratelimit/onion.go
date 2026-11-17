package ratelimit

import (
	"context"
	"net/http"
)

// OnionBucket is the one budget every request through the Tor hidden
// service shares: Tor hides the client, so there is no address to tell them
// apart by.
const OnionBucket = "onion"

type onionKey struct{}

// forwardingHeaders could name a client address or host; none of them can be
// believed on the onion listener, where every connection comes from tor.
var forwardingHeaders = []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For", "X-Forwarded-Host", "Forwarded", "True-Client-IP"}

// OnionHandler wraps the site for the hidden-service listener: it drops
// forwarding headers and marks each request as coming over Tor.
func OnionHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range forwardingHeaders {
			r.Header.Del(h)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), onionKey{}, true)))
	})
}

// IsOnion reports whether a request came through the hidden service.
func IsOnion(r *http.Request) bool {
	v, _ := r.Context().Value(onionKey{}).(bool)
	return v
}
