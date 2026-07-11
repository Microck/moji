package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type WebSearch struct {
	Client         *http.Client
	Instance       string
	KagiExecutable string
}

func (WebSearch) Name() string { return "websearch" }
func (source WebSearch) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	backends := 0
	backendEvents := make(chan Event)
	done := make(chan error, 2)
	if source.Instance != "" {
		backends++
		go func() { done <- source.searchSearXNG(ctx, query, formats, backendEvents) }()
	}
	if source.KagiExecutable != "" {
		backends++
		go func() {
			done <- (KagiCLI{Executable: source.KagiExecutable, Client: source.Client}).Search(ctx, query, formats, backendEvents)
		}()
	}
	if backends == 0 {
		return errors.New("websearch requires kagi-cli or a configured SearXNG instance")
	}
	seen := make(map[string]bool)
	var firstError error
	successes := 0
	for backends > 0 {
		select {
		case event := <-backendEvents:
			key := resultIdentity(event.Result)
			if event.Type != EventResult || !seen[key] {
				if event.Type == EventResult {
					seen[key] = true
				}
				out <- event
			}
		case err := <-done:
			backends--
			if err == nil {
				successes++
			} else if firstError == nil {
				firstError = err
			}
		}
	}
	if successes == 0 {
		return firstError
	}
	return nil
}

func (source WebSearch) searchSearXNG(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[format] = true
	}
	type searchResult struct {
		results []Result
		err     error
	}
	queries := webSearchQueries(query, formats)
	completed := make(chan searchResult, len(queries))
	for _, searchQuery := range queries {
		go func() {
			results, err := source.search(ctx, client, searchQuery, query, allowed)
			completed <- searchResult{results: results, err: err}
		}()
	}
	seen := make(map[string]bool)
	var firstError error
	successes := 0
	for range queries {
		search := <-completed
		if search.err != nil {
			if firstError == nil {
				firstError = search.err
			}
			continue
		}
		successes++
		for _, result := range search.results {
			key := resultIdentity(result)
			if !seen[key] {
				seen[key] = true
				out <- Event{Type: EventResult, Result: result}
			}
		}
	}
	if successes == 0 && firstError != nil {
		return firstError
	}
	return nil
}

func (source WebSearch) search(ctx context.Context, client *http.Client, searchQuery, fontQuery string, allowed map[string]bool) ([]Result, error) {
	parameters := url.Values{"q": {searchQuery}, "format": {"json"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source.Instance, "/")+"/search?"+parameters.Encode(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: websearch request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: websearch returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	var payload struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode websearch response: %v", ErrBadResponse, err)
	}
	results := make([]Result, 0, len(payload.Results))
	for _, item := range payload.Results {
		resolved, resolveErr := resolveDiscoveredURL(ctx, client, item.URL, fontQuery, allowed)
		if resolveErr == nil {
			results = append(results, resolved...)
		}
	}
	return results, nil
}

func webSearchQueries(query string, formats []string) []string {
	quoted := fmt.Sprintf("%q", query)
	queries := make([]string, 0, 6)
	primary := []string{"ttf", "otf"}
	for _, format := range primary {
		queries = append(queries, fmt.Sprintf("%q", query+"."+format))
	}
	queries = append(queries,
		"site:vk.com "+quoted,
		quoted+" \"index of\" "+strings.Join(prefixedFormats(formats), " OR "),
		"intitle:"+quoted+" github",
		quoted+" \"@font-face\" filetype:css",
	)
	return queries
}

func prefixedFormats(formats []string) []string {
	values := make([]string, 0, len(formats))
	for _, format := range formats {
		values = append(values, "."+strings.TrimPrefix(strings.ToLower(format), "."))
	}
	values = append(values, ".zip", ".tar.gz")
	return values
}
