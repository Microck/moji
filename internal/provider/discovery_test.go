package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveDiscoveredArchiveMembers(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	font, _ := writer.Create("Family/Example-Bold.otf")
	font.Write([]byte("OTTOfont"))
	writer.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write(archive.Bytes())
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(context.Background(), server.Client(), server.URL+"/family.zip", "Example", map[string]bool{"otf": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ArchiveMember != "Family/Example-Bold.otf" || results[0].Filename != "Example-Bold.otf" {
		t.Fatalf("results = %#v", results)
	}
}

func TestResolveDiscoveredStylesheetFonts(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte(`@font-face { src: url("../fonts/Example.woff2") format("woff2"); }`))
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(context.Background(), server.Client(), server.URL+"/css/family.css", "Example", map[string]bool{"woff2": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].URL != server.URL+"/fonts/Example.woff2" {
		t.Fatalf("results = %#v", results)
	}
}

func TestResolveDiscoveredStylesheetUsesDeclaredFormatWithoutExtension(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte(`@font-face { src: url("/font?id=example") format("woff2"); }`))
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(context.Background(), server.Client(), server.URL+"/family.css", "Example", map[string]bool{"woff2": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Format != "woff2" || results[0].Filename != "font.woff2" {
		t.Fatalf("results = %#v", results)
	}
}

func TestDiscoveryRejectsInsecureRedirect(t *testing.T) {
	t.Parallel()
	insecure := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte("font"))
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, insecure.URL+"/Example.woff2", http.StatusFound)
	}))
	defer secure.Close()
	if _, err := fetchDiscoveryContent(context.Background(), secure.Client(), secure.URL+"/family.css", 1024); err == nil {
		t.Fatal("expected insecure redirect rejection")
	}
}

func TestResolveDiscoveredURLConvertsGitHubBlobToRaw(t *testing.T) {
	results, err := resolveDiscoveredURL(context.Background(), nil, "https://github.com/acme/fonts/blob/main/Example.otf", "Example", map[string]bool{"otf": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].URL != "https://raw.githubusercontent.com/acme/fonts/main/Example.otf" {
		t.Fatalf("results = %#v", results)
	}
}
