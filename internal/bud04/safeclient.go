package bud04

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// dialCheck decides whether a resolved address may be connected to. A
// variable so tests can point the client at a local server.
var dialCheck = isPublicIP

// newMirrorClient returns an HTTP client for user-named URLs. Every name is
// resolved and every resulting address checked at dial time, so a DNS
// answer that changes between a check and the connection cannot steer the
// fetch inside, and every redirect hop goes through the same dial. The
// environment proxy is ignored for the same reason: a proxy would dial on
// the client's behalf and skip the check.
func newMirrorClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(addrs) == 0 {
				return nil, fmt.Errorf("mirror: %s does not resolve", host)
			}
			for _, a := range addrs {
				if !dialCheck(a.IP) {
					return nil, fmt.Errorf("mirror: %s resolves to a non-public address", host)
				}
			}
			var lastErr error
			for _, a := range addrs {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("mirror: too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("mirror: redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// reservedV4 lists IPv4 ranges that are never a public server: this host,
// private networks, carrier NAT, link-local, documentation and benchmark
// ranges, and the reserved top block.
var reservedV4 = mustCIDRs(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
)

// reservedV6 covers loopback, unspecified, unique-local, link-local,
// multicast, documentation, and the IPv4-embedding ranges (checked through
// their IPv4 part as well).
var reservedV6 = mustCIDRs(
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
	"2001:db8::/32",
	"64:ff9b::/96",
	"2002::/16",
)

func mustCIDRs(list ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// isPublicIP reports whether ip is an address a mirror may fetch from.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		for _, n := range reservedV4 {
			if n.Contains(v4) {
				return false
			}
		}
		return true
	}
	for _, n := range reservedV6 {
		if n.Contains(ip) {
			return false
		}
	}
	// NAT64 and 6to4 addresses embed an IPv4 address; judge that too.
	if len(ip) == net.IPv6len {
		if ip[0] == 0x20 && ip[1] == 0x02 {
			return isPublicIP(net.IPv4(ip[2], ip[3], ip[4], ip[5]))
		}
	}
	return true
}
