package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

type GetFonts struct {
	Client   *http.Client
	Endpoint string
}

func (GetFonts) Name() string { return "getfonts" }

func (source GetFonts) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := source.Endpoint
	if endpoint == "" {
		endpoint = "https://getfonts.cc/api/search"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+url.Values{"q": {query}}.Encode(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "moji-font-finder")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: getfonts request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: getfonts returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	var payload githubSearchResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("%w: decode getfonts response: %v", ErrBadResponse, err)
	}
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[format] = true
	}
	for _, item := range payload.Items {
		format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), ".")
		if !allowed[format] {
			continue
		}
		out <- Event{Type: EventResult, Result: Result{
			Name: query, Filename: filepath.Base(item.Name), Format: format,
			Source: "getfonts.cc/" + item.Repository.FullName, URL: item.HTMLURL,
			Trusted: false, License: "unknown",
		}}
	}
	return nil
}
