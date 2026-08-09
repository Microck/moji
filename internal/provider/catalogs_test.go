package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFontSquirrelSearchResolvesMatchingArchiveMembers(t *testing.T) {
	t.Parallel()
	archive := catalogTestZIP(t, map[string]string{
		"Amatic_SC/AmaticSC-Regular.ttf": "\x00\x01\x00\x00font",
		"Amatic_SC/AmaticSC-Bold.otf":    "OTTOfont",
		"Amatic_SC/readme.txt":           "license details",
	})
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/fontlist/all":
			response.Write([]byte(`[
				{"family_name":"Amatic SC","family_urlname":"amatic-sc"},
				{"family_name":"Amatic SC Pro","family_urlname":"amatic-sc-pro"},
				{"family_name":"Unrelated","family_urlname":"unrelated"}
			]`))
		case "/fonts/download/amatic-sc":
			response.Header().Set("Content-Type", "application/octet-stream")
			response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 4)
	err := (FontSquirrel{Client: server.Client(), Endpoint: server.URL}).Search(
		localDiscoveryContext(), "Amatic SC", []string{"ttf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("event count = %d, want 1", len(out))
	}
	got := (<-out).Result
	if got.Name != "Amatic SC" || got.Filename != "AmaticSC-Regular.ttf" || got.Format != "ttf" {
		t.Fatalf("result = %#v", got)
	}
	if got.ArchiveMember != "Amatic_SC/AmaticSC-Regular.ttf" || got.ArchiveFormat != "zip" {
		t.Fatalf("archive result = %#v", got)
	}
	if got.Source != strings.TrimPrefix(server.URL, "https://") || got.Trusted || got.License != "unknown" {
		t.Fatalf("provenance result = %#v", got)
	}
}

func TestFontshareSearchPreservesCatalogLicenseAndFormats(t *testing.T) {
	t.Parallel()
	archive := catalogTestZIP(t, map[string]string{
		"Satoshi/OTF/Satoshi-Regular.otf": "OTTOfont",
		"Satoshi/WEB/fonts/Satoshi.woff2": "wOF2font",
		"Satoshi/TTF/Satoshi-Regular.ttf": "\x00\x01\x00\x00font",
	})
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/fonts":
			if request.URL.Query().Get("limit") == "" || request.URL.Query().Get("order_by") != "popularity" {
				t.Fatalf("catalog query = %q", request.URL.RawQuery)
			}
			response.Write([]byte(`{"fonts":[
				{"name":"Satoshi","slug":"satoshi","license_type":"itf_ffl"},
				{"name":"General Sans","slug":"general-sans","license_type":"fontshare_ffl"}
			]}`))
		case "/v2/fonts/download/satoshi":
			response.Header().Set("Content-Type", "application/zip")
			response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 4)
	err := (Fontshare{Client: server.Client(), Endpoint: server.URL}).Search(
		localDiscoveryContext(), "Satoshi", []string{"otf", "woff2"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("event count = %d, want 2", len(out))
	}
	for len(out) > 0 {
		got := (<-out).Result
		if got.Name != "Satoshi" || got.Source != strings.TrimPrefix(server.URL, "https://") || got.Trusted || got.License != "itf_ffl" {
			t.Fatalf("result = %#v", got)
		}
		if got.Format != "otf" && got.Format != "woff2" {
			t.Fatalf("unexpected format in result = %#v", got)
		}
	}
}

func TestCatalogProvidersReportRateLimits(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	providers := []Provider{
		FontSquirrel{Client: server.Client(), Endpoint: server.URL},
		Fontshare{Client: server.Client(), Endpoint: server.URL},
	}
	for _, source := range providers {
		t.Run(source.Name(), func(t *testing.T) {
			err := source.Search(localDiscoveryContext(), "Example", []string{"otf"}, make(chan Event, 1))
			if !errors.Is(err, ErrRateLimited) {
				t.Fatalf("error = %v, want ErrRateLimited", err)
			}
		})
	}
}

func TestCatalogProvidersReportSiteChallenge(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Amzn-Waf-Action", "challenge")
		response.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	err := (FontSquirrel{Client: server.Client(), Endpoint: server.URL}).Search(
		localDiscoveryContext(), "Example", []string{"otf"}, make(chan Event, 1),
	)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("error = %v, want ErrBlocked", err)
	}
}

func TestCatalogProvidersReportArchiveHTTPFailures(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		status int
		want   error
	}{
		"blocked":      {status: http.StatusForbidden, want: ErrBlocked},
		"rate limited": {status: http.StatusTooManyRequests, want: ErrRateLimited},
		"bad response": {status: http.StatusInternalServerError, want: ErrBadResponse},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/api/fontlist/all":
					response.Write([]byte(`[{"family_name":"Example","family_urlname":"example"}]`))
				case "/fonts/download/example":
					response.WriteHeader(test.status)
				default:
					http.NotFound(response, request)
				}
			}))
			defer server.Close()

			err := (FontSquirrel{Client: server.Client(), Endpoint: server.URL}).Search(
				localDiscoveryContext(), "Example", []string{"otf"}, make(chan Event, 1),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestArchiveCatalogBlocksPrivateNetworkDestinations(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte(`[]`))
	}))
	defer server.Close()
	err := (FontSquirrel{Client: server.Client(), Endpoint: server.URL}).Search(
		context.Background(), "Example", []string{"otf"}, make(chan Event, 1),
	)
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("error = %v, want private-network rejection", err)
	}
}

func TestArchiveCatalogStopsBlockedSendOnCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	downloadStarted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- searchArchiveCatalog(ctx, http.DefaultClient, "Example", []string{"otf"},
			[]archiveCatalogEntry{{Name: "Example", Slug: "example", License: "test"}},
			func(string) string {
				close(downloadStarted)
				return "https://example.com/Example.otf"
			}, make(chan Event))
	}()
	<-downloadStarted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestArchiveCatalogMatchesPreferExactFamily(t *testing.T) {
	t.Parallel()
	entries := []archiveCatalogEntry{
		{Name: "Amatic SC Pro", Slug: "amatic-sc-pro"},
		{Name: "Amatic SC", Slug: "amatic-sc"},
		{Name: "SC Amatic", Slug: "sc-amatic"},
	}
	got := archiveCatalogMatches(entries, "Amatic SC")
	if len(got) != 1 || got[0].Name != "Amatic SC" {
		t.Fatalf("matches = %#v", got)
	}
}

func TestCatalogProviderCacheNamespacesIncludeEffectiveEndpoint(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		defaultNamespace string
		customNamespace  string
	}{
		"fontsquirrel": {
			defaultNamespace: (FontSquirrel{}).CacheNamespace(),
			customNamespace:  (FontSquirrel{Endpoint: "https://fonts.example.test/"}).CacheNamespace(),
		},
		"fontshare": {
			defaultNamespace: (Fontshare{}).CacheNamespace(),
			customNamespace:  (Fontshare{Endpoint: "https://fonts.example.test/"}).CacheNamespace(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if test.defaultNamespace == test.customNamespace {
				t.Fatalf("default and custom cache namespaces are both %q", test.defaultNamespace)
			}
			if strings.HasSuffix(test.customNamespace, "/") {
				t.Fatalf("custom cache namespace is not normalized: %q", test.customNamespace)
			}
		})
	}
}

func TestCatalogProviderCacheQueriesDeduplicateAdaptiveSpellings(t *testing.T) {
	t.Parallel()
	for _, source := range []interface{ CacheQuery(string) string }{FontSquirrel{}, Fontshare{}} {
		want := source.CacheQuery("Example Font")
		for _, query := range []string{"ExampleFont", "Example-Font", "Example_Font"} {
			if got := source.CacheQuery(query); got != want {
				t.Fatalf("CacheQuery(%q) = %q, want %q", query, got, want)
			}
		}
	}
}

func catalogTestZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
