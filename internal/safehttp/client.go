package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

var specialUsePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fec0::/10"),
}

// Constrain clones client and pins every connection to a DNS address that was
// checked as public. Custom TLS dial hooks are cleared because they otherwise
// bypass DialContext for HTTPS requests.
func Constrain(client *http.Client, allowPrivate bool) http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if allowPrivate {
		return clone
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if configured, ok := client.Transport.(*http.Transport); ok {
		transport = configured.Clone()
	}
	transport.Proxy = nil
	transport.DialContext = dialPublicAddress
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	clone.Transport = transport
	return clone
}

func dialPublicAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%s resolved to no addresses", host)
	}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return nil, fmt.Errorf("blocked non-public address for %s", host)
		}
	}
	dialer := &net.Dialer{}
	var firstError error
	for _, address := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		if firstError == nil {
			firstError = dialErr
		}
	}
	return nil, firstError
}

func publicIP(address net.IP) bool {
	parsed, ok := netip.AddrFromSlice(address)
	if !ok {
		return false
	}
	parsed = parsed.Unmap()
	if !parsed.IsGlobalUnicast() || parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || parsed.IsMulticast() {
		return false
	}
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(parsed) {
			return false
		}
	}
	return true
}
