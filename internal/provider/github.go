package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxGitHubResponseSize int64 = 20 << 20

const maxGitHubTreeResults = 500

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

type githubRepositorySearchResponse struct {
	Items []struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"items"`
}

type githubTreeResponse struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tree"`
}

type githubRelease struct {
	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
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
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[strings.TrimPrefix(strings.ToLower(format), ".")] = true
	}
	// GitHub Code Search requires authentication. Repository search, trees, and
	// releases retain a small unauthenticated allowance, so tokenless users skip
	// directly to that bounded fallback instead of making a guaranteed 401 call.
	useCodeSearch := source.Token != "" || source.Endpoint != ""
	for _, searchQuery := range githubSearchQueries(query, formats) {
		if !useCodeSearch {
			break
		}
		payload, err := source.search(ctx, client, endpoint, searchQuery, out)
		if err != nil {
			return err
		}
		emitted := 0
		for _, item := range payload.Items {
			format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), ".")
			if !allowed[format] {
				continue
			}
			branch := item.Repository.DefaultBranch
			if branch == "" {
				branch = branchFromHTMLURL(item.HTMLURL)
			}
			rawURL := githubRepositoryRawURL(item.Repository.FullName, branch, item.Path)
			out <- Event{Type: EventResult, Result: Result{
				Name: query, Filename: filepath.Base(item.Name), Format: format,
				Source: "github.com/" + item.Repository.FullName, URL: rawURL,
				Trusted: false, License: "unknown",
			}}
			emitted++
		}
		if emitted > 0 {
			return nil
		}
	}
	return source.searchRepositories(ctx, client, endpoint, query, allowed, out)
}

func (source GitHub) searchRepositories(ctx context.Context, client *http.Client, codeEndpoint, query string, allowed map[string]bool, out chan<- Event) error {
	base, err := url.Parse(codeEndpoint)
	if err != nil {
		return err
	}
	base.Path = strings.TrimSuffix(base.Path, "/search/code")
	searchURL := strings.TrimRight(base.String(), "/") + "/search/repositories"
	var repositories githubRepositorySearchResponse
	if err := source.getJSON(ctx, client, searchURL, url.Values{"q": {`"` + query + `" font`}, "per_page": {"2"}}, &repositories, out); err != nil {
		return err
	}
	for _, repository := range repositories.Items {
		branch := repository.DefaultBranch
		if branch == "" {
			branch = "HEAD"
		}
		apiRoot := strings.TrimRight(base.String(), "/") + "/repos/" + repository.FullName
		var tree githubTreeResponse
		treeErr := source.getJSON(ctx, client, apiRoot+"/git/trees/"+url.PathEscape(branch), url.Values{"recursive": {"1"}}, &tree, out)
		if stopGitHubFallback(treeErr) {
			return treeErr
		}
		if treeErr == nil {
			emittedFromTree := 0
			for _, item := range tree.Tree {
				format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Path)), ".")
				if item.Type != "blob" || !allowed[format] {
					continue
				}
				out <- Event{Type: EventResult, Result: Result{
					Name: query, Filename: filepath.Base(item.Path), Format: format, SizeBytes: item.Size,
					Source:  "github.com/" + repository.FullName,
					URL:     githubRepositoryRawURL(repository.FullName, branch, item.Path),
					Trusted: false, License: "unknown",
				}}
				emittedFromTree++
				if emittedFromTree >= maxGitHubTreeResults {
					break
				}
			}
		}
		var releases []githubRelease
		releaseErr := source.getJSON(ctx, client, apiRoot+"/releases", url.Values{"per_page": {"3"}}, &releases, out)
		if stopGitHubFallback(releaseErr) {
			return releaseErr
		}
		if releaseErr != nil {
			continue
		}
		for _, release := range releases {
			for _, asset := range release.Assets {
				results, resolveErr := resolveDiscoveredURL(ctx, client, asset.BrowserDownloadURL, query, allowed)
				if resolveErr != nil {
					continue
				}
				for _, result := range results {
					result.Source = "github.com/" + repository.FullName
					if result.SizeBytes == 0 && result.ArchiveMember == "" {
						result.SizeBytes = asset.Size
					}
					out <- Event{Type: EventResult, Result: result}
				}
			}
		}
	}
	return nil
}

func stopGitHubFallback(err error) bool {
	return errors.Is(err, ErrRateLimited) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (source GitHub) getJSON(ctx context.Context, client *http.Client, endpoint string, parameters url.Values, destination any, out chan<- Event) error {
	if encoded := parameters.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		retryAfter := githubRetryAfter(response.Header, time.Now())
		out <- Event{Type: EventStatus, Status: StateThrottled, Err: ErrRateLimited, RetryAfter: retryAfter}
		return ErrRateLimited
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return fmt.Errorf("%w: github returned HTTP %d", ErrNonRetryable, response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: github returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	if err := decodeGitHubJSON(response.Body, destination); err != nil {
		return fmt.Errorf("%w: decode github response: %v", ErrBadResponse, err)
	}
	return nil
}

func githubRepositoryRawURL(repository, branch, filePath string) string {
	return (&url.URL{
		Scheme: "https",
		Host:   "raw.githubusercontent.com",
		Path:   "/" + repository + "/" + branch + "/" + strings.TrimPrefix(filePath, "/"),
	}).String()
}

func decodeGitHubJSON(reader io.Reader, destination any) error {
	content, err := io.ReadAll(io.LimitReader(reader, maxGitHubResponseSize+1))
	if err != nil {
		return err
	}
	if int64(len(content)) > maxGitHubResponseSize {
		return fmt.Errorf("response exceeds %d bytes", maxGitHubResponseSize)
	}
	return json.Unmarshal(content, destination)
}

func (source GitHub) search(ctx context.Context, client *http.Client, endpoint, query string, out chan<- Event) (githubSearchResponse, error) {
	parameters := url.Values{"q": {query}, "per_page": {"100"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+parameters.Encode(), nil)
	if err != nil {
		return githubSearchResponse{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "moji-font-finder")
	if source.Token != "" {
		request.Header.Set("Authorization", "Bearer "+source.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return githubSearchResponse{}, fmt.Errorf("%w: github request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		retryAfter := githubRetryAfter(response.Header, time.Now())
		out <- Event{Type: EventStatus, Status: StateThrottled, Err: ErrRateLimited, RetryAfter: retryAfter}
		return githubSearchResponse{}, ErrRateLimited
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return githubSearchResponse{}, fmt.Errorf("%w: github returned HTTP %d", ErrNonRetryable, response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		return githubSearchResponse{}, fmt.Errorf("%w: github returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	var payload githubSearchResponse
	if err := decodeGitHubJSON(response.Body, &payload); err != nil {
		return githubSearchResponse{}, fmt.Errorf("%w: decode github response: %v", ErrBadResponse, err)
	}
	return payload, nil
}

func githubSearchQueries(query string, formats []string) []string {
	words := strings.Fields(query)
	variants := []string{
		strings.Join(words, ""),
		strings.Join(words, "-"),
		strings.Join(words, "_"),
		strings.ToLower(strings.Join(words, "")),
	}
	seen := make(map[string]bool)
	terms := make([]string, 0, len(variants)+len(variants)*len(formats))
	appendTerm := func(term string) {
		if term != "" && !seen[term] {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	for _, variant := range variants {
		appendTerm("filename:" + variant)
	}
	for _, variant := range variants {
		for _, format := range formats {
			appendTerm(fmt.Sprintf("%q", variant+"."+strings.TrimPrefix(strings.ToLower(format), ".")))
		}
	}
	exact := make([]string, 0, len(terms))
	length := 0
	for _, term := range terms {
		addition := len(term)
		if len(exact) > 0 {
			addition += len(" OR ")
		}
		if length+addition > 240 {
			break
		}
		exact = append(exact, term)
		length += addition
	}
	qualifiers := make([]string, 0, len(formats))
	seenQualifiers := make(map[string]bool)
	for _, format := range formats {
		qualifier := "extension:" + strings.TrimPrefix(strings.ToLower(format), ".")
		if !seenQualifiers[qualifier] {
			seenQualifiers[qualifier] = true
			qualifiers = append(qualifiers, qualifier)
		}
	}
	sort.Strings(qualifiers)
	return []string{strings.Join(exact, " OR "), query + " " + strings.Join(qualifiers, " OR ")}
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

func githubRetryAfter(header http.Header, now time.Time) time.Duration {
	if value := header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(value); err == nil && when.After(now) {
			return when.Sub(now)
		}
	}
	if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		when := time.Unix(reset, 0)
		if when.After(now) {
			return when.Sub(now)
		}
	}
	return 0
}
