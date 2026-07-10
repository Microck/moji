package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microck/moji/internal/provider"
)

func TestDownloadValidatesAndDeduplicates(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write(append([]byte("OTTO"), make([]byte, 32)...))
	}))
	defer server.Close()
	destination := t.TempDir()
	d := Downloader{Client: server.Client()}
	result := provider.Result{URL: server.URL, Source: "fixture", Filename: "../Example.otf", Format: "otf"}
	first, err := d.Download(context.Background(), result, destination)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Download(context.Background(), result, destination)
	if err != nil {
		t.Fatal(err)
	}
	if first.Existing || !second.Existing || second.SHA256 != first.SHA256 {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if first.Path != filepath.Join(destination, "Example.otf") {
		t.Fatalf("unsafe path: %s", first.Path)
	}
}

func TestDownloadRejectsInvalidContentWithoutResidue(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) { response.Write([]byte("html response")) }))
	defer server.Close()
	destination := t.TempDir()
	_, err := (Downloader{Client: server.Client()}).Download(context.Background(), provider.Result{URL: server.URL, Filename: "bad.ttf", Format: "ttf"}, destination)
	if err == nil {
		t.Fatal("expected invalid magic error")
	}
	if !strings.Contains(err.Error(), "No file was saved") || !strings.Contains(err.Error(), "try another source") {
		t.Fatalf("error does not explain recovery: %v", err)
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("temporary file left behind: %v, %v", entries, readErr)
	}
}

func TestDownloadRejectsInsecureHTTP(t *testing.T) {
	t.Parallel()
	_, err := (Downloader{}).Download(context.Background(), provider.Result{URL: "http://example.test/font.ttf", Filename: "font.ttf", Format: "ttf"}, t.TempDir())
	if err == nil {
		t.Fatal("expected insecure HTTP error")
	}
	if !strings.Contains(err.Error(), "blocked it before saving a file") {
		t.Fatalf("error does not explain preserved state: %v", err)
	}
}

func TestDownloadDeduplicatesSameBytesAcrossFilenames(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write(append([]byte("OTTO"), make([]byte, 32)...))
	}))
	defer server.Close()
	destination := t.TempDir()
	downloader := Downloader{Client: server.Client()}
	first, err := downloader.Download(context.Background(), provider.Result{URL: server.URL, Filename: "First.otf", Format: "otf"}, destination)
	if err != nil {
		t.Fatal(err)
	}
	second, err := downloader.Download(context.Background(), provider.Result{URL: server.URL, Filename: "Second.otf", Format: "otf"}, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Existing || second.Path != first.Path {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if _, err := os.Stat(filepath.Join(destination, "Second.otf")); !os.IsNotExist(err) {
		t.Fatalf("duplicate file was created: %v", err)
	}
}

func TestDownloadCapsRedirectsAtFive(t *testing.T) {
	t.Parallel()
	redirects := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirects++
		http.Redirect(response, request, "/next", http.StatusFound)
	}))
	defer server.Close()
	_, err := (Downloader{Client: server.Client()}).Download(context.Background(), provider.Result{URL: server.URL, Filename: "font.otf", Format: "otf"}, t.TempDir())
	if err == nil || redirects != 5 {
		t.Fatalf("redirects=%d err=%v", redirects, err)
	}
}

func TestDownloadRejectsBodyOverMaximumSize(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write(append([]byte("OTTO"), make([]byte, 64)...))
	}))
	defer server.Close()
	destination := t.TempDir()
	_, err := (Downloader{Client: server.Client(), MaxSize: 16}).Download(context.Background(), provider.Result{URL: server.URL, Filename: "large.otf", Format: "otf"}, destination)
	if err == nil {
		t.Fatal("expected maximum-size error")
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("oversized download left residue: %v, %v", entries, readErr)
	}
}

func TestDownloadRejectsHTTPSDowngradeRedirect(t *testing.T) {
	t.Parallel()
	insecure := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write(append([]byte("OTTO"), make([]byte, 16)...))
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, insecure.URL+"/font.otf", http.StatusFound)
	}))
	defer secure.Close()
	_, err := (Downloader{Client: secure.Client()}).Download(context.Background(), provider.Result{URL: secure.URL, Filename: "font.otf", Format: "otf"}, t.TempDir())
	if err == nil {
		t.Fatal("expected HTTPS downgrade redirect to be rejected")
	}
}
