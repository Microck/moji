package download

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microck/moji/internal/provider"
)

func TestDownloadExtractsAndValidatesArchiveMember(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	member, _ := writer.Create("Family/Example-Regular.otf")
	member.Write(append([]byte("OTTO"), make([]byte, 32)...))
	writer.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write(archive.Bytes())
	}))
	defer server.Close()
	destination := t.TempDir()
	file, err := (Downloader{Client: server.Client()}).Download(context.Background(), provider.Result{
		Filename: "Example-Regular.otf", Format: "otf", URL: server.URL, Source: "fixture",
		ArchiveFormat: "zip", ArchiveMember: "Family/Example-Regular.otf",
	}, destination)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(file.Path)
	if err != nil || string(content[:4]) != "OTTO" {
		t.Fatalf("content=%x err=%v", content, err)
	}
}

func TestDownloadClassifiesMalformedArchiveAsInvalidContent(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte("not a zip archive"))
	}))
	defer server.Close()
	_, err := (Downloader{Client: server.Client()}).Download(context.Background(), provider.Result{
		URL: server.URL, Filename: "Example-Regular.otf", Format: "otf",
		ArchiveFormat: "zip", ArchiveMember: "Example-Regular.otf",
	}, t.TempDir())
	if err == nil || !IsInvalidContent(err) || InvalidContentURL(err) != server.URL {
		t.Fatalf("malformed archive classification = %v", err)
	}
}

func TestValidateLegacyFontFormats(t *testing.T) {
	t.Parallel()
	pfb := []byte{0x80, 0x01, 0x04, 0x00, 0x00, 0x00, 'f', 'o', 'n', 't', 0x80, 0x03}
	pfm := make([]byte, 117)
	binary.LittleEndian.PutUint16(pfm[:2], 0x0100)
	binary.LittleEndian.PutUint32(pfm[2:6], uint32(len(pfm)))
	dfont := make([]byte, 64)
	binary.BigEndian.PutUint32(dfont[0:4], 16)
	binary.BigEndian.PutUint32(dfont[4:8], 24)
	binary.BigEndian.PutUint32(dfont[8:12], 8)
	binary.BigEndian.PutUint32(dfont[12:16], 40)
	copy(dfont[32:], "sfnt")
	for format, content := range map[string][]byte{"pfb": pfb, "pfm": pfm, "dfont": dfont} {
		if err := ValidateMagic(format, content); err != nil {
			t.Errorf("%s validation failed: %v", format, err)
		}
		if err := ValidateMagic(format, content[:len(content)-1]); err == nil {
			t.Errorf("truncated %s passed validation", format)
		}
	}
}

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
	if !IsInvalidContent(err) {
		t.Fatalf("invalid font content must be classifiable for URL health caching: %v", err)
	}
	if !strings.Contains(err.Error(), "No file was saved") || !strings.Contains(err.Error(), "try another source") {
		t.Fatalf("error does not explain recovery: %v", err)
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("temporary file left behind: %v, %v", entries, readErr)
	}
}

func TestDownloadBatchLeavesDestinationUnchangedWhenAnyFontIsInvalid(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/valid.otf" {
			response.Write(append([]byte("OTTO"), make([]byte, 32)...))
			return
		}
		response.Write([]byte("not a font"))
	}))
	defer server.Close()
	destination := t.TempDir()
	marker := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := (Downloader{Client: server.Client()}).DownloadBatch(context.Background(), []provider.Result{
		{URL: server.URL + "/valid.otf", Filename: "Family-Regular.otf", Format: "otf"},
		{URL: server.URL + "/invalid.otf", Filename: "Family-Bold.otf", Format: "otf"},
	}, destination)
	if err == nil || !IsInvalidContent(err) {
		t.Fatalf("expected classifiable invalid-content failure, got %v", err)
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("family failure changed destination: entries=%v err=%v", entries, readErr)
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
