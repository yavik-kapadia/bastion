package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// parseTrustedProxies turns a list of IPs / CIDRs into networks. A bare IP is
// treated as a single-host network (/32 or /128).
func parseTrustedProxies(entries []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			nets = append(nets, n)
			continue
		}
		ip := net.ParseIP(e)
		if ip == nil {
			return nil, fmt.Errorf("trusted_proxies: %q is not a valid IP or CIDR", e)
		}
		bits := 128
		if ip.To4() != nil {
			ip = ip.To4()
			bits = 32
		}
		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return nets, nil
}

func ipTrusted(ip net.IP, trusted []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// realIP rewrites r.RemoteAddr from X-Forwarded-For / X-Real-IP, but only when
// the direct peer is a trusted proxy. Unlike chi's middleware.RealIP, headers
// from untrusted peers are ignored, so clients cannot spoof their address to
// dodge the per-IP login rate limit.
//
// For X-Forwarded-For, the right-most address that is not itself a trusted
// proxy is used; entries to its left are client-controlled.
func realIP(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) > 0 {
				if ip := clientIP(r, trusted); ip != "" {
					r.RemoteAddr = net.JoinHostPort(ip, "0")
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP returns the forwarded client address, or "" to keep RemoteAddr.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !ipTrusted(net.ParseIP(host), trusted) {
		return ""
	}

	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		parts := strings.Split(strings.Join(xff, ","), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				// Malformed hop — stop rather than trust anything further left.
				return ""
			}
			if !ipTrusted(ip, trusted) {
				return ip.String()
			}
		}
		return ""
	}

	if xrip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); xrip != nil {
		return xrip.String()
	}
	return ""
}
