package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

type KagiCLI struct {
	Executable string
	Client     *http.Client
}

func (KagiCLI) Name() string { return "kagi" }

func (source KagiCLI) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	executable := source.Executable
	if executable == "" {
		executable = "kagi"
	}
	searchQuery := "(" + query + " font " + strings.Join(kagiQueryFormats(formats), " ") + " zip css) OR (" + strings.Join(fontIndexQueries(query), " OR ") + ")"
	command := exec.CommandContext(ctx, executable, "search", "--format", "json", "--error-format", "json", "--limit", "20", searchQuery)
	content, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			message := strings.TrimSpace(string(exitError.Stderr))
			if message != "" {
				return fmt.Errorf("%w: kagi search: %s", ErrUnavailable, message)
			}
		}
		return fmt.Errorf("%w: kagi search: %v", ErrUnavailable, err)
	}
	var response struct {
		Data []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"data"`
	}
	if err := json.Unmarshal(content, &response); err != nil {
		return fmt.Errorf("%w: decode kagi response: %v", ErrBadResponse, err)
	}
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[strings.ToLower(format)] = true
	}
	for _, item := range response.Data {
		resolved, resolveErr := resolveDiscoveredURL(ctx, source.Client, item.URL, query, allowed)
		if resolveErr == nil {
			for _, result := range resolved {
				out <- Event{Type: EventResult, Result: result}
			}
		}
	}
	return nil
}

func kagiQueryFormats(formats []string) []string {
	modern := make([]string, 0, len(formats))
	for _, format := range formats {
		switch strings.TrimPrefix(strings.ToLower(format), ".") {
		case "otf", "ttf", "woff", "woff2":
			modern = append(modern, format)
		}
	}
	if len(modern) > 0 {
		return modern
	}
	return formats
}

func githubRawURL(value *url.URL) *url.URL {
	parts := strings.Split(strings.Trim(value.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "blob" {
		return nil
	}
	return &url.URL{
		Scheme: "https",
		Host:   "raw.githubusercontent.com",
		Path:   "/" + strings.Join(append(parts[:2], parts[3:]...), "/"),
	}
}
