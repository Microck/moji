package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type GitHub struct {
	Client   *http.Client
	Endpoint string
	Token    string
}

func (GitHub) Name() string { return "github" }

type githubSearchResponse struct {
	Items []struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		HTMLURL    string `json:"html_url"`
		Repository struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
		} `json:"repository"`
	} `json:"items"`
}

func (source GitHub) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	client := source.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := source.Endpoint
	if endpoint == "" {
		endpoint = "https://api.github.com/search/code"
	}
	for _, format := range formats {
		parameters := url.Values{"q": {fmt.Sprintf("%s extension:%s", query, format)}, "per_page": {"30"}}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+parameters.Encode(), nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		request.Header.Set("User-Agent", "moji-font-finder")
		if source.Token != "" {
			request.Header.Set("Authorization", "Bearer "+source.Token)
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("%w: github request: %v", ErrUnavailable, err)
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
			response.Body.Close()
			out <- Event{Type: EventStatus, Status: StateThrottled, Err: ErrRateLimited, RetryAfter: retryAfter}
			return ErrRateLimited
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("%w: github returned HTTP %d", ErrBadResponse, response.StatusCode)
		}
		var payload githubSearchResponse
		err = json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if err != nil {
			return fmt.Errorf("%w: decode github response: %v", ErrBadResponse, err)
		}
		for _, item := range payload.Items {
			branch := item.Repository.DefaultBranch
			if branch == "" {
				branch = branchFromHTMLURL(item.HTMLURL)
			}
			rawURL := "https://raw.githubusercontent.com/" + item.Repository.FullName + "/" + branch + "/" + strings.TrimPrefix(item.Path, "/")
			out <- Event{Type: EventResult, Result: Result{
				Name: query, Filename: filepath.Base(item.Name),
				Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), "."),
				Source: "github.com/" + item.Repository.FullName, URL: rawURL,
				Trusted: false, License: "unknown",
			}}
		}
	}
	return nil
}

func branchFromHTMLURL(value string) string {
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if (part == "blob" || part == "raw") && index+1 < len(parts) {
			return parts[index+1]
		}
	}
	return "HEAD"
}

func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(value)
	if err == nil {
		return time.Duration(seconds) * time.Second
	}
	return 0
}
