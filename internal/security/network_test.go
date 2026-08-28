package security

import (
	"context"
	"net"
	"testing"
)

func TestBlockedIPs(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"169.254.169.254",
		"0.0.0.0",
		"::1",
		"fc00::1",
		"fe80::1",
	}
	for _, host := range blocked {
		if err := NewGuard().ResolveAndValidateHost(context.Background(), host); err == nil {
			t.Errorf("expected block for %s", host)
		}
	}
}

func TestPublicIPAllowed(t *testing.T) {
	if err := NewGuard().ResolveAndValidateHost(context.Background(), "8.8.8.8"); err != nil {
		t.Fatalf("expected allow for public IP: %v", err)
	}
}

func TestDNSResolveBlocked(t *testing.T) {
	g := NetworkGuard{
		BlockPrivateNetworks: true,
		LookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.168.1.50")}, nil
		},
	}
	if err := g.ResolveAndValidateHost(context.Background(), "internal.example.com"); err == nil {
		t.Fatal("expected block for DNS resolving to private IP")
	}
}

func TestDNSResolvePublicAllowed(t *testing.T) {
	g := NetworkGuard{
		BlockPrivateNetworks: true,
		LookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		},
	}
	if err := g.ResolveAndValidateHost(context.Background(), "example.com"); err != nil {
		t.Fatalf("expected allow for public DNS: %v", err)
	}
}

func TestEmptyHostRejected(t *testing.T) {
	if err := NewGuard().ResolveAndValidateHost(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestGuardDisabled(t *testing.T) {
	g := NetworkGuard{BlockPrivateNetworks: false}
	if err := g.ResolveAndValidateHost(context.Background(), "127.0.0.1"); err != nil {
		t.Fatalf("expected allow when guard disabled: %v", err)
	}
}

func NewGuard() NetworkGuard {
	return NetworkGuard{BlockPrivateNetworks: true}
}
