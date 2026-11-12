// Package netguard blocks outbound connections to private, loopback and
// other non-public addresses. MCP servers that fetch URLs chosen by a model
// (fetch, http, acp, graphql) dial through it so a prompt can't point them at
// the host, the container network or cloud metadata endpoints.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// ErrBlocked is wrapped by every error returned for a refused address.
var ErrBlocked = errors.New("address is not allowed")

// blockedPrefixes lists every range that must never be dialed. It covers the
// IANA special-purpose registries for IPv4 and IPv6 except ranges that are
// globally routable in practice. IPv4-mapped, NAT64 and 6to4 addresses are
// unwrapped and checked against the IPv4 list in IsBlocked.
var blockedPrefixes = mustPrefixes(
	// IPv4
	"0.0.0.0/8",       // "this network"; 0.0.0.0 reaches localhost on Linux
	"10.0.0.0/8",      // private
	"100.64.0.0/10",   // carrier-grade NAT, also Tailscale
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, cloud metadata
	"172.16.0.0/12",   // private
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // 6to4 relay anycast (deprecated)
	"192.168.0.0/16",  // private
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, includes 255.255.255.255
	// IPv6
	"::/128",        // unspecified, reaches localhost
	"::1/128",       // loopback
	"100::/64",      // discard-only
	"2001::/23",     // IETF protocol assignments, incl. Teredo
	"2001:db8::/32", // documentation
	"fc00::/7",      // unique local
	"fe80::/10",     // link-local
	"fec0::/10",     // site-local (deprecated)
	"ff00::/8",      // multicast
)

var (
	nat64Prefix      = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalPrefix = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFourPrefix  = netip.MustParsePrefix("2002::/16")
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// IsBlocked reports whether addr must not be dialed. An invalid address is
// treated as blocked.
func IsBlocked(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.WithZone("")
	// ::ffff:a.b.c.d is the IPv4 address a.b.c.d.
	addr = addr.Unmap()
	if addr.Is6() {
		raw := addr.As16()
		switch {
		case nat64Prefix.Contains(addr):
			// 64:ff9b::a.b.c.d is translated to a.b.c.d by a NAT64 gateway.
			return IsBlocked(netip.AddrFrom4([4]byte{raw[12], raw[13], raw[14], raw[15]}))
		case nat64LocalPrefix.Contains(addr):
			// Local-use NAT64 prefix: the embedded address is site-specific.
			return true
		case sixToFourPrefix.Contains(addr):
			// 2002:aabb:ccdd::/48 tunnels to a.b.c.d (bytes 2-5).
			return IsBlocked(netip.AddrFrom4([4]byte{raw[2], raw[3], raw[4], raw[5]}))
		}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// IsBlockedIP is IsBlocked for a net.IP.
func IsBlockedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return IsBlocked(addr)
}

// blockedHostnames are refused before DNS so a hosts-file entry can't be used.
var blockedHostnames = map[string]bool{
	"localhost":     true,
	"ip6-localhost": true,
	"ip6-loopback":  true,
}

// Resolver looks up a host's addresses. *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer dials TCP connections only to public addresses. Use its DialContext
// as an http.Transport's DialContext.
type Dialer struct {
	// AllowedHosts are trusted hostnames (lower case) that skip every check,
	// for internal services the server must reach, such as an S3 endpoint.
	AllowedHosts map[string]bool
	// Resolver defaults to net.DefaultResolver.
	Resolver Resolver
	// Dial defaults to a zero net.Dialer. Tests replace it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// DialContext resolves addr, refuses it if any resolved address is blocked,
// and otherwise dials the first resolved address. Dialing the checked address
// rather than the hostname stops DNS rebinding between check and connect.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	dial := d.Dial
	if dial == nil {
		var nd net.Dialer
		dial = nd.DialContext
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if d.AllowedHosts[lower] {
		return dial(ctx, network, net.JoinHostPort(host, port))
	}
	if blockedHostnames[lower] || strings.HasSuffix(lower, ".localhost") {
		return nil, fmt.Errorf("access to %q: %w", host, ErrBlocked)
	}

	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		r := d.Resolver
		if r == nil {
			r = net.DefaultResolver
		}
		addrs, err = r.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("no addresses for %q", host)
		}
	}
	for _, a := range addrs {
		if IsBlocked(a) {
			return nil, fmt.Errorf("access to private address %s: %w", a.Unmap(), ErrBlocked)
		}
	}
	return dial(ctx, network, net.JoinHostPort(addrs[0].Unmap().String(), port))
}
