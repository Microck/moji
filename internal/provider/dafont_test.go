package provider

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDaFontSearchFindsPeanutsGangArchive(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	font, err := writer.Create("Peanuts_Gang_Dingbats.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := font.Write([]byte("\x00\x01\x00\x00font")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search.php":
			if got := request.URL.Query().Get("q"); got != "Peanuts" {
				t.Fatalf("catalog query = %q, want Peanuts", got)
			}
			response.Header().Set("Content-Type", "text/html")
			response.Write([]byte(`
				<div class="lv1left dfbg">Crappy <span class="highlight">Peanuts</span> by <a>Mike</a></div>
				<div><a class="dl" href="//dl.dafont.com/dl/?f=crappypeanuts">Download</a></div>
				<div class="lv1left dfbg"><span class="highlight">Peanuts</span> Gang Dings by <a>Jbs</a></div>
				<div><a class="dl" href="//dl.dafont.com/dl/?f=peanuts_gang_dings">Download</a></div>
			`))
		case "/dl/":
			if got := request.URL.Query().Get("f"); got != "peanuts_gang_dings" {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.Header().Set("Content-Type", "application/zip")
			response.Write(archive.Bytes())
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 4)
	err = (DaFont{Client: server.Client(), Endpoint: server.URL}).Search(
		localDiscoveryContext(), "Peanuts", []string{"ttf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 {
		t.Fatalf("result count = %d, want 1", len(out))
	}
	got := (<-out).Result
	if got.Name != "Peanuts Gang Dings" || got.Filename != "Peanuts_Gang_Dingbats.ttf" || got.Format != "ttf" {
		t.Fatalf("result = %#v", got)
	}
	if got.ArchiveFormat != "zip" || got.ArchiveMember != "Peanuts_Gang_Dingbats.ttf" || got.License != "unknown" {
		t.Fatalf("archive result = %#v", got)
	}
}

func TestParseDaFontCatalogKeepsHighlightedFamilyNameAndSlug(t *testing.T) {
	t.Parallel()
	content := `<div class="lv1left dfbg">Salted <span class="highlight">Peanuts</span>&nbsp;<span title="Accents">&agrave;</span> by <a>Goma Shin</a></div>
		<div><a class="dl" href="//dl.dafont.com/dl/?f=salted_peanuts&amp;x=1">Download</a></div>`
	entries := parseDaFontCatalog(content)
	if len(entries) != 1 || entries[0].Name != "Salted Peanuts" || entries[0].Slug != "salted_peanuts" {
		t.Fatalf("entries = %#v", entries)
	}
	if !strings.Contains((DaFont{}).CacheNamespace(), dafontEndpoint) {
		t.Fatalf("cache namespace = %q", (DaFont{}).CacheNamespace())
	}
}

func TestDaFontCacheQueryPreservesAdaptiveSpellings(t *testing.T) {
	t.Parallel()
	source := DaFont{}
	if source.CacheQuery("ProximaNova") == source.CacheQuery("Proxima Nova") {
		t.Fatal("adaptive spellings must use distinct DaFont cache keys")
	}
	if source.CacheQuery("  Proxima   Nova ") != source.CacheQuery("proxima nova") {
		t.Fatal("cache keys should normalize case and repeated whitespace")
	}
}
