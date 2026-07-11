package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Fontsource struct {
	Client   *http.Client
	Endpoint string
}

func (Fontsource) Name() string { return "fontsource" }

func (source Fontsource) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := source.Endpoint
	if endpoint == "" {
		endpoint = "https://api.fontsource.org/v1/fonts"
	}
	id := strings.ToLower(strings.Join(strings.Fields(query), "-"))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: Fontsource request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: Fontsource returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	var payload struct {
		Family   string `json:"family"`
		License  string `json:"license"`
		Variable bool   `json:"variable"`
		Variants map[string]map[string]map[string]struct {
			URL map[string]string `json:"url"`
		} `json:"variants"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("%w: decode Fontsource response: %v", ErrBadResponse, err)
	}
	allowed := formatSet(formats)
	for weight, styles := range payload.Variants {
		for style, subsets := range styles {
			for _, subset := range subsets {
				for format, directURL := range subset.URL {
					format = strings.ToLower(format)
					if !allowed[format] || !isHTTPSURL(directURL) {
						continue
					}
					normalizedWeight := registryWeight(weight)
					filename := syntheticFilename(payload.Family, normalizedWeight, style, format)
					out <- Event{Type: EventResult, Result: Result{
						Name: payload.Family, Filename: filename, Format: format, Weight: normalizedWeight,
						Source: "fontsource.org", URL: directURL, Trusted: true, License: payload.License,
						Variable: payload.Variable && strings.Contains(strings.ToLower(directURL), "variable"),
					}}
				}
			}
		}
	}
	return nil
}

type GoogleFonts struct {
	Client   *http.Client
	Endpoint string
}

type RegistrySearch struct {
	Fontsource  Fontsource
	GoogleFonts GoogleFonts
}

func (RegistrySearch) Name() string { return "registry" }

func (source RegistrySearch) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	backends := []Provider{source.Fontsource, source.GoogleFonts}
	type backendResult struct {
		name string
		err  error
	}
	done := make(chan backendResult, len(backends))
	for _, backend := range backends {
		go func() {
			done <- backendResult{name: backend.Name(), err: backend.Search(ctx, query, formats, out)}
		}()
	}
	errorsByBackend := make([]string, 0, len(backends))
	completed := 0
	for range backends {
		result := <-done
		if result.err != nil {
			errorsByBackend = append(errorsByBackend, result.name+": "+result.err.Error())
			continue
		}
		completed++
	}
	if completed == 0 {
		return fmt.Errorf("%w: %s", ErrUnavailable, strings.Join(errorsByBackend, "; "))
	}
	return nil
}

func (GoogleFonts) Name() string { return "googlefonts" }

func (source GoogleFonts) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	allowed := formatSet(formats)
	if !allowed["woff2"] && !allowed["woff"] {
		return nil
	}
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := source.Endpoint
	if endpoint == "" {
		endpoint = "https://fonts.googleapis.com/css2"
	}
	parameters := url.Values{"family": {query}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+parameters.Encode(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 moji-font-finder")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: Google Fonts request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: Google Fonts returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxDiscoveryStylesheetSize+1))
	if err != nil {
		return fmt.Errorf("%w: read Google Fonts response: %v", ErrBadResponse, err)
	}
	if int64(len(content)) > maxDiscoveryStylesheetSize {
		return fmt.Errorf("%w: Google Fonts stylesheet exceeds %d bytes", ErrBadResponse, maxDiscoveryStylesheetSize)
	}
	seen := make(map[string]bool)
	for _, match := range cssSource.FindAllSubmatch(content, -1) {
		directURL := string(match[1])
		parsed, parseErr := url.Parse(directURL)
		if parseErr != nil || parsed.Scheme != "https" || seen[directURL] {
			continue
		}
		format := cssFontFormat(parsed.Path, string(match[2]))
		if !allowed[format] {
			continue
		}
		seen[directURL] = true
		out <- Event{Type: EventResult, Result: Result{
			Name: query, Filename: syntheticFilename(query, "regular", "normal", format),
			Format: format, Weight: "regular", Source: "fonts.google.com", URL: directURL,
			Trusted: true, License: "unknown",
		}}
	}
	return nil
}

func formatSet(formats []string) map[string]bool {
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[strings.TrimPrefix(strings.ToLower(format), ".")] = true
	}
	return allowed
}

func isHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func syntheticFilename(family, weight, style, format string) string {
	name := strings.Join(strings.Fields(family), "-")
	if weight != "" {
		name += "-" + weight
	}
	if style != "" && style != "normal" {
		name += "-" + style
	}
	return name + "." + format
}

func registryWeight(value string) string {
	switch value {
	case "100":
		return "thin"
	case "300":
		return "light"
	case "400":
		return "regular"
	case "500":
		return "medium"
	case "600":
		return "semibold"
	case "700", "800":
		return "bold"
	case "900":
		return "black"
	default:
		return value
	}
}
