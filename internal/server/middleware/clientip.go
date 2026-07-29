package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/Abir66/mdfly/internal/server/httpx"
)

// connectingIPHeader carries the real client IP through Cloudflare; the TCP peer
// behind the proxy is a Cloudflare edge address, not the caller (ADR-0013).
const connectingIPHeader = "CF-Connecting-IP"

// cloudflareRanges is Cloudflare's published edge address space
// (https://www.cloudflare.com/ips/). A peer inside these prefixes reached the
// origin directly from the edge and may speak for a client via
// connectingIPHeader. Refresh alongside the same list in deploy/Caddyfile.
var cloudflareRanges = mustParsePrefixes(
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
)

// subjectSeparator delimits the segments of a Redis rate-limit key, so an IPv6
// address must not carry it verbatim into a subject.
const subjectSeparator = ":"

// subjectIPSeparator replaces subjectSeparator inside an IPv6 address, keeping a
// key readable as rl:ip:<addr>:<window>:<step>.
const subjectIPSeparator = "."

// rateLimitSubject identifies who to charge a request to (ADR-0013): the Edit
// Token when the caller carries one, otherwise the client IP. A token is
// fingerprinted rather than used verbatim — the subject becomes a Redis key in a
// third-party store, and a credential does not belong there. An IPv6 address has
// its colons flattened so it cannot be mistaken for extra key segments.
func rateLimitSubject(r *http.Request) string {
	if token := httpx.BearerToken(r.Header.Get("Authorization")); token != "" {
		return "token:" + fingerprint(token)
	}
	return "ip:" + strings.ReplaceAll(clientIP(r), subjectSeparator, subjectIPSeparator)
}

// fingerprintLen is how much of the token digest identifies a subject. 16 hex
// chars (64 bits) makes a collision between two live Edit Tokens implausible.
const fingerprintLen = 16

func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:fingerprintLen]
}

// clientIP resolves the caller's address: the connecting-IP header when the peer
// is a trusted proxy, else the peer itself.
func clientIP(r *http.Request) string {
	peer := peerIP(r.RemoteAddr)
	claimed := strings.TrimSpace(r.Header.Get(connectingIPHeader))
	if claimed == "" || !fromTrustedProxy(peer) {
		return peer
	}
	return claimed
}

// peerIP is the host part of addr, which http.Server sets to "ip:port".
func peerIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// fromTrustedProxy reports whether a peer may speak for a client through
// connectingIPHeader. Two peers qualify: a Cloudflare edge, and a loopback or
// private address — the origin's own TLS terminator, which sits between the edge
// and this process and is responsible for stripping the header when its own peer
// is not Cloudflare (see deploy/Caddyfile). A private source address cannot be
// routed in from the internet, so nothing else can claim it.
func fromTrustedProxy(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if addr.IsLoopback() || addr.IsPrivate() {
		return true
	}
	for _, prefix := range cloudflareRanges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func mustParsePrefixes(raw ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(raw))
	for _, cidr := range raw {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			panic("middleware: invalid Cloudflare range " + cidr)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}
