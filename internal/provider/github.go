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
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxGitHubResponseSize int64 = 20 << 20

const maxGitHubTreeResults = 500

const maxGitHubContextRepositories = 3

const maxGitHubDirectRepositories = 1

const maxGitHubCodeQueries = 10

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

type githubRepository struct {
	FullName      string
	DefaultBranch string
	HintPath      string
}

type githubTreeResponse struct {
	Truncated bool `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tree"`
}

type githubContentItem struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type githubRelease struct {
	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func (source GitHub) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	if !claimGitHubSearch(ctx, query) {
		return ErrSearchSkipped
	}
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
	totalEmitted := 0
	seenDirectURLs := make(map[string]bool)
	inspectedRepositories := make(map[string]bool)
	directRepositoryInspections := 0
	for _, searchQuery := range githubSearchQueries(query, formats) {
		if !useCodeSearch {
			break
		}
		payload, remaining, err := source.search(ctx, client, endpoint, searchQuery, out)
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
			if seenDirectURLs[rawURL] {
				continue
			}
			seenDirectURLs[rawURL] = true
			out <- Event{Type: EventResult, Result: Result{
				Name: query, Filename: filepath.Base(item.Name), Format: format,
				Source: "github.com/" + item.Repository.FullName, URL: rawURL,
				Trusted: false, License: "unknown",
			}}
			emitted++
		}
		totalEmitted += emitted
		candidates := githubCandidateRepositories(payload)
		repositories := make([]githubRepository, 0, len(candidates))
		for _, repository := range candidates {
			if len(inspectedRepositories) >= maxGitHubContextRepositories {
				break
			}
			if inspectedRepositories[repository.FullName] {
				continue
			}
			format := strings.TrimPrefix(strings.ToLower(filepath.Ext(repository.HintPath)), ".")
			isDirectRepository := githubFontExtension(format)
			if isDirectRepository && directRepositoryInspections >= maxGitHubDirectRepositories {
				continue
			}
			if isDirectRepository {
				directRepositoryInspections++
			}
			inspectedRepositories[repository.FullName] = true
			repositories = append(repositories, repository)
		}
		if len(repositories) > 0 {
			emitted, inspectErr := source.inspectRepositoryTrees(ctx, client, endpoint, repositories, query, allowed, seenDirectURLs, out)
			if inspectErr != nil {
				return inspectErr
			}
			if emitted > 0 {
				return nil
			}
		}
		if remaining == 0 {
			break
		}
	}
	if totalEmitted > 0 {
		return nil
	}
	return source.searchRepositories(ctx, client, endpoint, query, allowed, out)
}

func githubCandidateRepositories(payload githubSearchResponse) []githubRepository {
	seen := make(map[string]bool)
	repositories := make([]githubRepository, 0, maxGitHubContextRepositories)
	for _, includeFonts := range []bool{false, true} {
		for _, item := range payload.Items {
			format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), ".")
			if githubFontExtension(format) != includeFonts {
				continue
			}
			name := item.Repository.FullName
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			repositories = append(repositories, githubRepository{FullName: name, DefaultBranch: item.Repository.DefaultBranch, HintPath: item.Path})
			if len(repositories) == maxGitHubContextRepositories {
				return repositories
			}
		}
	}
	return repositories
}

func githubFontExtension(format string) bool {
	switch format {
	case "ttf", "otf", "woff2", "woff", "dfont", "pfb", "pfm":
		return true
	default:
		return false
	}
}

func (source GitHub) inspectRepositoryTrees(ctx context.Context, client *http.Client, codeEndpoint string, repositories []githubRepository, query string, allowed map[string]bool, seenResultURLs map[string]bool, out chan<- Event) (int, error) {
	base, err := url.Parse(codeEndpoint)
	if err != nil {
		return 0, err
	}
	base.Path = strings.TrimSuffix(base.Path, "/search/code")
	emitted := 0
	for _, repository := range repositories {
		if emitted >= maxGitHubTreeResults {
			return emitted, nil
		}
		branch := repository.DefaultBranch
		if branch == "" {
			branch = "HEAD"
		}
		apiRoot := strings.TrimRight(base.String(), "/") + "/repos/" + repository.FullName
		var tree githubTreeResponse
		treeErr := source.getJSON(ctx, client, apiRoot+"/git/trees/"+url.PathEscape(branch), url.Values{"recursive": {"1"}}, &tree, out)
		if stopGitHubFallback(treeErr) {
			return emitted, treeErr
		}
		if treeErr != nil {
			continue
		}
		seenPaths := make(map[string]bool)
		for _, item := range tree.Tree {
			format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Path)), ".")
			if item.Type != "blob" || !allowed[format] || !githubPathMatchesQuery(item.Path, query) {
				continue
			}
			rawURL := githubRepositoryRawURL(repository.FullName, branch, item.Path)
			seenPaths[item.Path] = true
			if seenResultURLs[rawURL] {
				continue
			}
			seenResultURLs[rawURL] = true
			out <- Event{Type: EventResult, Result: Result{
				Name: query, Filename: filepath.Base(item.Path), Format: format, SizeBytes: item.Size,
				Source: "github.com/" + repository.FullName,
				URL:    rawURL, Trusted: false, License: "unknown",
			}}
			emitted++
			if emitted >= maxGitHubTreeResults {
				return emitted, nil
			}
		}
		if tree.Truncated {
			repository.DefaultBranch = branch
			contentCount, contentErr := source.inspectRepositoryDirectory(ctx, client, base.String(), repository, query, allowed, seenPaths, seenResultURLs, maxGitHubTreeResults-emitted, out)
			if stopGitHubFallback(contentErr) {
				return emitted, contentErr
			}
			emitted += contentCount
		}
	}
	return emitted, nil
}

func (source GitHub) inspectRepositoryDirectory(ctx context.Context, client *http.Client, apiBase string, repository githubRepository, query string, allowed map[string]bool, seenPaths, seenResultURLs map[string]bool, limit int, out chan<- Event) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	directory := filepath.ToSlash(filepath.Dir(repository.HintPath))
	if directory == "." {
		directory = ""
	}
	branch := repository.DefaultBranch
	if branch == "" {
		branch = "HEAD"
	}
	endpoint := strings.TrimRight(apiBase, "/") + "/repos/" + githubEscapePath(repository.FullName) + "/contents"
	if directory != "" {
		endpoint += "/" + githubEscapePath(strings.TrimLeft(directory, "/"))
	}
	var items []githubContentItem
	if err := source.getJSON(ctx, client, endpoint, url.Values{"ref": {branch}}, &items, out); err != nil {
		return 0, err
	}
	emitted := 0
	for _, item := range items {
		format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Path)), ".")
		if item.Type != "file" || seenPaths[item.Path] || !allowed[format] || !githubPathMatchesQuery(item.Path, query) {
			continue
		}
		rawURL := githubRepositoryRawURL(repository.FullName, branch, item.Path)
		if seenResultURLs != nil && seenResultURLs[rawURL] {
			continue
		}
		if seenResultURLs != nil {
			seenResultURLs[rawURL] = true
		}
		out <- Event{Type: EventResult, Result: Result{
			Name: query, Filename: filepath.Base(item.Path), Format: format, SizeBytes: item.Size,
			Source: "github.com/" + repository.FullName,
			URL:    rawURL, Trusted: false, License: "unknown",
		}}
		emitted++
		if emitted >= limit {
			break
		}
	}
	return emitted, nil
}

func githubEscapePath(value string) string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func githubPathMatchesQuery(pathValue, query string) bool {
	compactPath := githubCompact(pathValue)
	words := strings.FieldsFunc(strings.ToLower(query), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
	aliases := map[string][]string{
		"premier": {"premr"}, "regular": {"reg", "rg"}, "bold": {"bd"},
		"italic": {"it"}, "condensed": {"cond", "cn"}, "narrow": {"nr"}, "light": {"lt"},
	}
	variants := []string{""}
	for _, word := range words {
		options := append([]string{word}, aliases[word]...)
		next := make([]string, 0, min(32, len(variants)*len(options)))
		for _, prefix := range variants {
			for _, option := range options {
				if len(next) == 32 {
					break
				}
				next = append(next, prefix+option)
			}
		}
		variants = next
	}
	for _, variant := range variants {
		if variant != "" && strings.Contains(compactPath, variant) {
			return true
		}
	}
	return false
}

func githubCompact(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, value)
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
		emittedFromTree := 0
		seenPaths := make(map[string]bool)
		if treeErr == nil {
			for _, item := range tree.Tree {
				format := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Path)), ".")
				if item.Type != "blob" || !allowed[format] || !githubPathMatchesQuery(item.Path, query) {
					continue
				}
				out <- Event{Type: EventResult, Result: Result{
					Name: query, Filename: filepath.Base(item.Path), Format: format, SizeBytes: item.Size,
					Source:  "github.com/" + repository.FullName,
					URL:     githubRepositoryRawURL(repository.FullName, branch, item.Path),
					Trusted: false, License: "unknown",
				}}
				seenPaths[item.Path] = true
				emittedFromTree++
				if emittedFromTree >= maxGitHubTreeResults {
					break
				}
			}
			if tree.Truncated {
				contentCount, contentErr := source.inspectRepositoryDirectory(ctx, client, base.String(), githubRepository{
					FullName: repository.FullName, DefaultBranch: branch,
				}, query, allowed, seenPaths, nil, maxGitHubTreeResults-emittedFromTree, out)
				if stopGitHubFallback(contentErr) {
					return contentErr
				}
				emittedFromTree += contentCount
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
					result.FamilyGroup = result.Source
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

func (source GitHub) search(ctx context.Context, client *http.Client, endpoint, query string, out chan<- Event) (githubSearchResponse, int, error) {
	parameters := url.Values{"q": {query}, "per_page": {"100"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+parameters.Encode(), nil)
	if err != nil {
		return githubSearchResponse{}, -1, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "moji-font-finder")
	if source.Token != "" {
		request.Header.Set("Authorization", "Bearer "+source.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return githubSearchResponse{}, -1, fmt.Errorf("%w: github request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		retryAfter := githubRetryAfter(response.Header, time.Now())
		out <- Event{Type: EventStatus, Status: StateThrottled, Err: ErrRateLimited, RetryAfter: retryAfter}
		return githubSearchResponse{}, 0, ErrRateLimited
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return githubSearchResponse{}, -1, fmt.Errorf("%w: github returned HTTP %d", ErrNonRetryable, response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		return githubSearchResponse{}, -1, fmt.Errorf("%w: github returned HTTP %d", ErrBadResponse, response.StatusCode)
	}
	var payload githubSearchResponse
	if err := decodeGitHubJSON(response.Body, &payload); err != nil {
		return githubSearchResponse{}, -1, fmt.Errorf("%w: decode github response: %v", ErrBadResponse, err)
	}
	remaining := -1
	if value, parseErr := strconv.Atoi(response.Header.Get("X-RateLimit-Remaining")); parseErr == nil {
		remaining = value
	}
	return payload, remaining, nil
}

func githubSearchQueries(query string, formats []string) []string {
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[strings.TrimPrefix(strings.ToLower(format), ".")] = true
	}
	orderedFormats := make([]string, 0, len(allowed))
	for _, preferred := range []string{"ttf", "otf", "woff2", "woff", "dfont", "pfb", "pfm"} {
		if allowed[preferred] {
			orderedFormats = append(orderedFormats, preferred)
		}
	}
	name := strings.TrimSpace(query)
	if name == "" || len(orderedFormats) == 0 {
		return nil
	}
	primaryCount := min(2, len(orderedFormats))
	queries := make([]string, 0, maxGitHubCodeQueries)
	names := []string{name}
	words := strings.Fields(name)
	if len(words) > 1 {
		names = append(names, strings.Join(words, "-"), strings.Join(words, "_"), strings.Join(words, ""))
	}
	for _, format := range orderedFormats[:primaryCount] {
		queries = append(queries, name+"."+format)
	}
	for _, format := range orderedFormats[:primaryCount] {
		queries = append(queries, name+" ."+format)
	}
	for _, filename := range names[1:] {
		for _, format := range orderedFormats[:primaryCount] {
			queries = append(queries, filename+"."+format)
		}
	}
	unique := uniqueStrings(queries)
	if len(unique) > maxGitHubCodeQueries {
		unique = unique[:maxGitHubCodeQueries]
	}
	return unique
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
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
