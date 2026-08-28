// Package security provides SSRF-related protections for outbound fetches.
package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// NetworkGuard enforces SSRF-related address policy: it rejects private and
// other unsafe IP ranges that a public fetch target should never reach.
type NetworkGuard struct {
	BlockPrivateNetworks bool
	LookupIP             func(ctx context.Context, network, host string) ([]net.IP, error)
}

// ResolveAndValidateHost resolves host (IP or name) and rejects it if it maps
// to an unsafe (private/loopback/etc.) address. An empty host is rejected.
func (g NetworkGuard) ResolveAndValidateHost(ctx context.Context, host string) error {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return errors.New("host is required")
	}
	if !g.BlockPrivateNetworks {
		return nil
	}

	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("blocked unsafe address: %s", ip.String())
		}
		return nil
	}

	lookup := g.LookupIP
	if lookup == nil {
		var resolver net.Resolver
		lookup = resolver.LookupIP
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve host %q: no addresses returned", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("blocked unsafe address: %s (%s)", ip.String(), host)
		}
	}
	return nil
}

// isBlockedIP reports whether ip is in a range that should not be reachable
// from a public fetch endpoint.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast()
}
