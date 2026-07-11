package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func localDiscoveryContext() context.Context {
	return context.WithValue(context.Background(), privateDiscoveryContextKey{}, true)
}

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
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/family.zip", "Example", map[string]bool{"otf": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ArchiveMember != "Family/Example-Bold.otf" || results[0].Filename != "Example-Bold.otf" || results[0].FamilyGroup == "" || results[0].FamilyGroup == server.URL+"/family.zip" {
		t.Fatalf("results = %#v", results)
	}
}

func TestResolveDiscoveredArchiveByContentWhenExtensionIsMissing(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	font, _ := writer.Create("Family/Example-Regular.otf")
	font.Write([]byte("OTTOfont"))
	writer.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		response.Write(archive.Bytes())
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/download?id=family", "Example", map[string]bool{"otf": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ArchiveFormat != "zip" || results[0].ArchiveMember != "Family/Example-Regular.otf" {
		t.Fatalf("results = %#v", results)
	}
}

func TestResolveDiscoveredURLDoesNotTreatHTMLPageAsDownload(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html")
		response.Write([]byte("<html>PK\\x03\\x04 download</html>"))
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/font-page", "Example", map[string]bool{"otf": true})
	if err != nil || len(results) != 0 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestResolveDiscoveredURLRejectsHTMLWithArchiveSuffix(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html")
		response.Write([]byte("<html>not an archive</html>"))
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/font.zip", "Example", map[string]bool{"otf": true})
	if err != nil || len(results) != 0 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestDiscoveryBlocksPrivateNetworkDestinations(t *testing.T) {
	t.Parallel()
	results, err := resolveDiscoveredURL(context.Background(), http.DefaultClient, "https://127.0.0.1/font-download", "Example", map[string]bool{"otf": true})
	if err == nil || len(results) != 0 || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestResolveDiscoveredArchiveAcceptsNonstandardDownloadContentType(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	font, _ := writer.Create("Example-Regular.otf")
	font.Write([]byte("OTTOfont"))
	writer.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/force-download")
		response.Write(archive.Bytes())
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/download", "Example", map[string]bool{"otf": true})
	if err != nil || len(results) != 1 || results[0].ArchiveFormat != "zip" {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestSniffArchiveFormatRecognizesSupportedSignatures(t *testing.T) {
	t.Parallel()
	tarContent := make([]byte, 262)
	copy(tarContent[257:], "ustar")
	for name, test := range map[string]struct {
		content []byte
		want    string
	}{
		"zip":  {content: []byte{'P', 'K', 3, 4}, want: "zip"},
		"tgz":  {content: []byte{0x1f, 0x8b, 8, 0}, want: "tgz"},
		"tar":  {content: tarContent, want: "tar"},
		"html": {content: []byte("<html>"), want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := sniffArchiveFormat(test.content); got != test.want {
				t.Fatalf("sniffArchiveFormat() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveDiscoveredStylesheetFonts(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte(`@font-face { src: url("../fonts/Example.woff2") format("woff2"); }`))
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/css/family.css", "Example", map[string]bool{"woff2": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].URL != server.URL+"/fonts/Example.woff2" || results[0].FamilyGroup == "" || results[0].FamilyGroup == server.URL+"/css/family.css" {
		t.Fatalf("results = %#v", results)
	}
}

func TestResolveDiscoveredStylesheetUsesDeclaredFormatWithoutExtension(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte(`@font-face { src: url("/font?id=example") format("woff2"); }`))
	}))
	defer server.Close()
	results, err := resolveDiscoveredURL(localDiscoveryContext(), server.Client(), server.URL+"/family.css", "Example", map[string]bool{"woff2": true})
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
	if _, err := fetchDiscoveryContent(localDiscoveryContext(), secure.Client(), secure.URL+"/family.css", 1024); err == nil {
		t.Fatal("expected insecure redirect rejection")
	}
}

func TestResolveDiscoveredURLConvertsGitHubBlobToRaw(t *testing.T) {
	results, err := resolveDiscoveredURL(context.Background(), nil, "https://github.com/acme/fonts/blob/main/Example.otf", "Example", map[string]bool{"otf": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].URL != "https://raw.githubusercontent.com/acme/fonts/main/Example.otf" || results[0].FamilyGroup != "github.com/acme/fonts" {
		t.Fatalf("results = %#v", results)
	}
}
