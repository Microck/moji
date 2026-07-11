package safehttp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestConstrainedTransportBlocksPrivateDestinations(t *testing.T) {
	t.Parallel()
	client := Constrain(nil, false)
	transport := client.Transport.(*http.Transport)
	for _, destination := range []string{"127.0.0.1:443", "localhost:443"} {
		_, err := transport.DialContext(context.Background(), "tcp", destination)
		if err == nil || !strings.Contains(err.Error(), "non-public address") {
			t.Fatalf("destination=%s err=%v", destination, err)
		}
	}
}

func TestPublicIPRejectsSpecialUseNetworks(t *testing.T) {
	t.Parallel()
	for _, address := range []string{
		"100.64.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"64:ff9b::1", "64:ff9b:1::1", "100::1", "2001::1", "2002::1", "3fff::1", "fec0::1",
	} {
		if publicIP(net.ParseIP(address)) {
			t.Errorf("special-use address %s was treated as public", address)
		}
	}
}

func TestPublicIPAcceptsGloballyReachableAddresses(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !publicIP(net.ParseIP(address)) {
			t.Errorf("globally reachable address %s was rejected", address)
		}
	}
}

func TestConstrainedTransportClearsCustomTLSDialers(t *testing.T) {
	t.Parallel()
	called := false
	configured := http.DefaultTransport.(*http.Transport).Clone()
	configured.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		called = true
		return nil, nil
	}
	configured.DialTLS = func(string, string) (net.Conn, error) {
		called = true
		return nil, nil
	}
	client := Constrain(&http.Client{Transport: configured}, false)
	transport := client.Transport.(*http.Transport)
	if transport.DialTLSContext != nil || transport.DialTLS != nil {
		t.Fatal("custom TLS dialer survived the public-address constraint")
	}
	_, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:443")
	if err == nil || called {
		t.Fatalf("err=%v custom TLS dialer called=%t", err, called)
	}
}

func TestConstrainKeepsTLSConfiguration(t *testing.T) {
	t.Parallel()
	configured := http.DefaultTransport.(*http.Transport).Clone()
	configured.TLSClientConfig = &tls.Config{ServerName: "fonts.example"}
	client := Constrain(&http.Client{Transport: configured}, false)
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig == configured.TLSClientConfig || transport.TLSClientConfig.ServerName != "fonts.example" {
		t.Fatal("TLS configuration was not safely cloned")
	}
}
