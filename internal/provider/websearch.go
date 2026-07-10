package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

type WebSearch struct {
	Client   *http.Client
	Instance string
}

func (WebSearch) Name() string { return "websearch" }
func (source WebSearch) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	if source.Instance == "" {
		return errors.New("websearch requires a SearXNG instance in config")
	}
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[format] = true
	}
	parameters := url.Values{"q": {query + " font " + strings.Join(formats, " ")}, "format": {"json"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source.Instance, "/")+"/search?"+parameters.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: websearch request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: websearch returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	var payload struct {
		Results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("%w: decode websearch response: %v", ErrBadResponse, err)
	}
	for _, item := range payload.Results {
		parsed, err := url.Parse(item.URL)
		if err != nil {
			continue
		}
		filename := filepath.Base(parsed.Path)
		format := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
		if !allowed[format] {
			continue
		}
		out <- Event{Type: EventResult, Result: Result{
			Name: query, Filename: filename, Format: format, Source: parsed.Host,
			URL: item.URL, Trusted: false, License: "unknown",
		}}
	}
	return nil
}
