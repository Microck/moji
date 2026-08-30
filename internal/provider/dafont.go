package provider

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const dafontEndpoint = "https://www.dafont.com"

var (
	dafontEntryPattern = regexp.MustCompile(`(?is)<div class="lv1left dfbg">(.*?)</div>.*?<a class="dl"[^>]*href="([^"]+)"`)
	dafontBadgePattern = regexp.MustCompile(`(?is)<span[^>]*title="(?:Accents|Euro)"[^>]*>.*?</span>`)
	dafontTagPattern   = regexp.MustCompile(`(?s)<[^>]+>`)
)

// DaFont uses a searchable HTML catalog, but its download links are direct ZIP
// responses. Keeping that small, known page parser here lets the archives pass
// through the same HTTPS, size, path, and format checks as every other source.
type DaFont struct {
	Client   *http.Client
	Endpoint string
}

func (DaFont) Name() string { return "dafont" }

func (source DaFont) baseURL() string {
	if endpoint := strings.TrimRight(source.Endpoint, "/"); endpoint != "" {
		return endpoint
	}
	return dafontEndpoint
}

func (source DaFont) CacheNamespace() string {
	return source.Name() + "\x00" + source.baseURL()
}

func (DaFont) CacheQuery(query string) string { return archiveCatalogKey(query) }

func (source DaFont) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := source.baseURL()
	entries, err := fetchDaFontCatalog(ctx, client, baseURL+"/search.php?"+url.Values{"q": {query}}.Encode())
	if err != nil {
		return fmt.Errorf("DaFont catalog: %w", err)
	}
	downloadBase := "https://dl.dafont.com"
	if source.Endpoint != "" {
		downloadBase = baseURL
	}
	return searchArchiveCatalog(ctx, client, query, formats, entries, func(slug string) string {
		return downloadBase + "/dl/?" + url.Values{"f": {slug}}.Encode()
	}, out)
}

func fetchDaFontCatalog(ctx context.Context, client *http.Client, endpoint string) ([]archiveCatalogEntry, error) {
	clientCopy := constrainedDiscoveryClient(ctx, client)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "moji-font-finder")
	response, err := clientCopy.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, providerHTTPStatusError(response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxArchiveCatalogSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read catalog: %v", ErrBadResponse, err)
	}
	if len(content) > maxArchiveCatalogSize {
		return nil, fmt.Errorf("%w: catalog exceeds %d bytes", ErrBadResponse, maxArchiveCatalogSize)
	}
	return parseDaFontCatalog(string(content)), nil
}

func parseDaFontCatalog(content string) []archiveCatalogEntry {
	matches := dafontEntryPattern.FindAllStringSubmatch(content, -1)
	entries := make([]archiveCatalogEntry, 0, len(matches))
	seen := make(map[string]bool)
	for _, match := range matches {
		nameMarkup := dafontBadgePattern.ReplaceAllString(match[1], "")
		name := strings.Join(strings.Fields(html.UnescapeString(dafontTagPattern.ReplaceAllString(nameMarkup, ""))), " ")
		if before, _, found := strings.Cut(name, " by "); found {
			name = strings.TrimSpace(before)
		}
		reference, err := url.Parse(html.UnescapeString(match[2]))
		if err != nil {
			continue
		}
		slug := reference.Query().Get("f")
		if name == "" || slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		entries = append(entries, archiveCatalogEntry{Name: name, Slug: slug, License: "unknown"})
	}
	return entries
}
