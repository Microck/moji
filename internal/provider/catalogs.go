package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

const (
	maxArchiveCatalogSize    = 8 << 20
	maxArchiveCatalogMatches = 3
	fontSquirrelEndpoint     = "https://www.fontsquirrel.com"
	fontshareEndpoint        = "https://api.fontshare.com"
)

type FontSquirrel struct {
	Client   *http.Client
	Endpoint string
}

func (FontSquirrel) Name() string { return "fontsquirrel" }

func (source FontSquirrel) baseURL() string {
	if endpoint := strings.TrimRight(source.Endpoint, "/"); endpoint != "" {
		return endpoint
	}
	return fontSquirrelEndpoint
}

func (source FontSquirrel) CacheNamespace() string {
	return source.Name() + "\x00" + source.baseURL()
}

func (FontSquirrel) CacheQuery(query string) string {
	return archiveCatalogKey(query)
}

func (source FontSquirrel) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := source.baseURL()
	var payload []struct {
		Name string `json:"family_name"`
		Slug string `json:"family_urlname"`
	}
	if err := fetchArchiveCatalog(ctx, client, baseURL+"/api/fontlist/all", &payload); err != nil {
		return fmt.Errorf("Font Squirrel catalog: %w", err)
	}
	entries := make([]archiveCatalogEntry, 0, len(payload))
	for _, family := range payload {
		entries = append(entries, archiveCatalogEntry{Name: family.Name, Slug: family.Slug, License: "unknown"})
	}
	return searchArchiveCatalog(ctx, client, query, formats, entries, func(slug string) string {
		return baseURL + "/fonts/download/" + url.PathEscape(slug)
	}, out)
}

type Fontshare struct {
	Client   *http.Client
	Endpoint string
}

func (Fontshare) Name() string { return "fontshare" }

func (source Fontshare) baseURL() string {
	if endpoint := strings.TrimRight(source.Endpoint, "/"); endpoint != "" {
		return endpoint
	}
	return fontshareEndpoint
}

func (source Fontshare) CacheNamespace() string {
	return source.Name() + "\x00" + source.baseURL()
}

func (Fontshare) CacheQuery(query string) string {
	return archiveCatalogKey(query)
}

func (source Fontshare) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := source.baseURL()
	parameters := url.Values{"limit": {"1000"}, "offset": {"0"}, "order_by": {"popularity"}}
	var payload struct {
		Fonts []struct {
			Name    string `json:"name"`
			Slug    string `json:"slug"`
			License string `json:"license_type"`
		} `json:"fonts"`
	}
	if err := fetchArchiveCatalog(ctx, client, baseURL+"/v2/fonts?"+parameters.Encode(), &payload); err != nil {
		return fmt.Errorf("Fontshare catalog: %w", err)
	}
	entries := make([]archiveCatalogEntry, 0, len(payload.Fonts))
	for _, family := range payload.Fonts {
		entries = append(entries, archiveCatalogEntry{Name: family.Name, Slug: family.Slug, License: family.License})
	}
	return searchArchiveCatalog(ctx, client, query, formats, entries, func(slug string) string {
		return baseURL + "/v2/fonts/download/" + url.PathEscape(slug)
	}, out)
}

type archiveCatalogEntry struct {
	Name    string
	Slug    string
	License string
}

func fetchArchiveCatalog(ctx context.Context, client *http.Client, endpoint string, target any) error {
	client = constrainedDiscoveryClient(ctx, client)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "moji-font-finder")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if strings.EqualFold(response.Header.Get("X-Amzn-Waf-Action"), "challenge") {
		return fmt.Errorf("%w: site challenge", ErrBlocked)
	}
	switch response.StatusCode {
	case http.StatusOK:
	default:
		return providerHTTPStatusError(response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxArchiveCatalogSize+1))
	if err != nil {
		return fmt.Errorf("%w: read catalog: %v", ErrBadResponse, err)
	}
	if len(content) > maxArchiveCatalogSize {
		return fmt.Errorf("%w: catalog exceeds %d bytes", ErrBadResponse, maxArchiveCatalogSize)
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("%w: decode catalog: %v", ErrBadResponse, err)
	}
	return nil
}

func searchArchiveCatalog(ctx context.Context, client *http.Client, query string, formats []string, entries []archiveCatalogEntry, downloadURL func(string) string, out chan<- Event) error {
	allowed := formatSet(formats)
	var firstErr error
	emitted := 0
	for _, entry := range archiveCatalogMatches(entries, query) {
		results, err := resolveDiscoveredURL(ctx, client, downloadURL(entry.Slug), entry.Name, allowed)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, result := range results {
			result.License = entry.License
			if result.License == "" {
				result.License = "unknown"
			}
			select {
			case out <- Event{Type: EventResult, Result: result}:
			case <-ctx.Done():
				return ctx.Err()
			}
			emitted++
		}
	}
	if emitted == 0 && firstErr != nil {
		return firstErr
	}
	return nil
}

func archiveCatalogMatches(entries []archiveCatalogEntry, query string) []archiveCatalogEntry {
	queryKey := archiveCatalogKey(query)
	if queryKey == "" {
		return nil
	}
	type match struct {
		entry archiveCatalogEntry
		rank  int
	}
	matches := make([]match, 0)
	for _, entry := range entries {
		nameKey := archiveCatalogKey(entry.Name)
		rank := 2
		switch {
		case nameKey == "" || !strings.Contains(nameKey, queryKey):
			continue
		case nameKey == queryKey:
			rank = 0
		case strings.HasPrefix(nameKey, queryKey):
			rank = 1
		}
		matches = append(matches, match{entry: entry, rank: rank})
	}
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].rank != matches[right].rank {
			return matches[left].rank < matches[right].rank
		}
		return len(matches[left].entry.Name) < len(matches[right].entry.Name)
	})
	if len(matches) > 0 && matches[0].rank == 0 {
		matches = matches[:1]
	} else if len(matches) > maxArchiveCatalogMatches {
		matches = matches[:maxArchiveCatalogMatches]
	}
	result := make([]archiveCatalogEntry, len(matches))
	for index, candidate := range matches {
		result[index] = candidate.entry
	}
	return result
}

func archiveCatalogKey(value string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}
