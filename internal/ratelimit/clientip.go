package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// forwardedHeaders are believed only when the request comes from a trusted
// proxy. CF-Connecting-IP is what a Cloudflare tunnel sets; X-Real-IP is the
// single-address header nginx-style proxies set. Both carry one address set
// by the proxy itself, unlike X-Forwarded-For, which clients can prepend to.
var forwardedHeaders = []string{"CF-Connecting-IP", "X-Real-IP"}

type proxyNet struct{ *net.IPNet }

func parseProxies(entries []string) ([]proxyNet, error) {
	out := make([]proxyNet, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			_, n, err := net.ParseCIDR(e)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: %w", e, err)
			}
			out = append(out, proxyNet{n})
			continue
		}
		ip := net.ParseIP(e)
		if ip == nil {
			return nil, fmt.Errorf("trusted proxy %q is not an IP address or CIDR", e)
		}
		bits := 128
		if ip.To4() != nil {
			bits = 32
		}
		out = append(out, proxyNet{&net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}})
	}
	return out, nil
}

// ClientIP returns the bucket address for a request: the forwarded client
// address when the request comes from loopback or a trusted proxy and carries
// one, otherwise the peer address. IPv6 is bucketed by /64, since one host
// usually holds a whole /64 and could otherwise rotate through it.
func (l *Limiter) ClientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return host
	}
	l.mu.Lock()
	proxies := l.proxies
	l.mu.Unlock()
	if ip.IsLoopback() || containsIP(proxies, ip) {
		for _, h := range forwardedHeaders {
			if fwd := net.ParseIP(strings.TrimSpace(r.Header.Get(h))); fwd != nil {
				return BucketAddr(fwd)
			}
		}
	}
	return BucketAddr(ip)
}

func containsIP(nets []proxyNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// BucketAddr reduces an address to the string the limiter buckets by.
func BucketAddr(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
