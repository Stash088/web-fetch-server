package fetch

import (
	"context"
	"fmt"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
)

// tlsDialChrome dials addr and performs a TLS handshake presenting a
// Chrome-like ClientHello (via uTLS), which defeats JA3-based bot
// fingerprinting that blocks the default Go TLS stack.
func tlsDialChrome(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	uconn := utls.UClient(conn, &utls.Config{
		ServerName: host,
		MinVersion: utls.VersionTLS12,
	}, utls.HelloChrome_133)
	if err := uconn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return uconn, nil
}
