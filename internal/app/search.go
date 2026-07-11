package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/microck/moji/internal/aggregator"
	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/download"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
	"github.com/microck/moji/internal/tui"
)

func (application App) search(ctx context.Context, current config.Config, query string, formats []string, parsed options) ([]provider.Result, []string, error) {
	events, providerCount, err := application.searchEvents(ctx, current, query, formats, parsed)
	if err != nil {
		return nil, nil, err
	}
	results := make([]provider.Result, 0)
	completed := make(map[string]bool)
	failuresByProvider := make(map[string]string)
	started := time.Now()
	for event := range events {
		if event.Type == provider.EventResult {
			results = append(results, event.Result)
		} else if event.Status == provider.StateDone {
			completed[event.Provider] = true
			delete(failuresByProvider, event.Provider)
		} else if event.Status == provider.StateFailed {
			failuresByProvider[event.Provider] = provider.DescribeFailure(event.Provider, event.Err)
		}
		if parsed.debug {
			fmt.Fprintf(application.Stderr, "debug: provider=%s state=%d count=%d retry_after=%s\n", event.Provider, event.Status, event.Count, event.RetryAfter)
		}
	}
	results = provider.UniqueResults(results)
	failures := make([]string, 0, len(failuresByProvider))
	for _, failure := range failuresByProvider {
		failures = append(failures, failure)
	}
	sort.Strings(failures)
	if parsed.verbose {
		fmt.Fprintf(application.Stderr, "searched %d providers in %s; %d unique results\n", providerCount, time.Since(started).Round(time.Millisecond), len(results))
	}
	if len(results) == 0 && len(completed) == 0 {
		return nil, failures, fmt.Errorf("no providers could complete the search. %s. Check your connection and provider settings, then try again", strings.Join(failures, "; "))
	}
	return results, failures, nil
}

func (application App) searchEvents(ctx context.Context, current config.Config, query string, formats []string, parsed options) (<-chan provider.Event, int, error) {
	cacheDirectory, err := cache.DefaultDirectory()
	if err != nil {
		return nil, 0, err
	}
	store := cache.Store{Directory: cacheDirectory, TTL: time.Duration(current.CacheTTLSeconds) * time.Second}
	invalidURLs, _ := store.InvalidURLs()
	available := map[string]provider.Provider{
		"getfonts": provider.GetFonts{Client: application.Client, Endpoint: current.Providers["getfonts"].Instance},
		"registry": provider.RegistrySearch{
			Fontsource:  provider.Fontsource{Client: application.Client},
			GoogleFonts: provider.GoogleFonts{Client: application.Client},
		},
	}
	if len(current.SourcePlugins) > 0 {
		available["plugins"] = provider.PluginSearch{Client: application.Client, Paths: current.SourcePlugins}
	}
	githubSettings := current.Providers["github"]
	available["github"] = provider.GitHub{Client: application.Client, Endpoint: githubSettings.Instance, Token: current.Token()}
	if web := current.Providers["websearch"]; web.Enabled {
		executable, _ := exec.LookPath("kagi")
		if web.Instance != "" || executable != "" {
			available["websearch"] = provider.WebSearch{Client: application.Client, Instance: web.Instance, KagiExecutable: executable}
		}
	}
	selected, err := selectProviders(available, current, parsed.providers)
	if err != nil {
		return nil, 0, err
	}
	cached := make([]provider.Provider, 0, len(selected))
	for _, source := range selected {
		cached = append(cached, cache.CachedProvider{Source: source, Store: store, Bypass: parsed.noCache})
	}
	searchCtx, cancel := context.WithTimeout(ctx, time.Duration(current.SearchTimeoutSeconds)*time.Second)
	aggregate := aggregator.Aggregator{Providers: cached, Policies: current.Policies()}
	events := make(chan provider.Event)
	go func() {
		defer close(events)
		defer cancel()
		forward := func(searchQuery string) (relevantCount, completedCount int) {
			for event := range aggregate.Search(searchCtx, searchQuery, formats) {
				if event.Type == provider.EventResult && invalidURLs[resultHealthKey(event.Result)] {
					continue
				}
				if event.Type == provider.EventResult && rank.Relevance(event.Result, searchQuery) > 0 {
					relevantCount++
				}
				if event.Type == provider.EventStatus && event.Status == provider.StateDone {
					completedCount++
				}
				events <- event
			}
			return relevantCount, completedCount
		}
		for _, searchQuery := range rank.AdaptiveQueries(query) {
			resultCount, completedCount := forward(searchQuery)
			if resultCount > 0 || completedCount == 0 {
				break
			}
		}
	}()
	return events, len(selected), nil
}

func (application App) runInteractive(ctx context.Context, current config.Config, query string, formats []string, parsed options) int {
	events, _, err := application.searchEvents(ctx, current, query, formats, parsed)
	if err != nil {
		return application.fail(err, 1)
	}
	model := tui.NewLiveModel(events, application.interactiveDownloader(ctx, parsed), colorEnabled(), query, strings.ToLower(parsed.weight), current.Ranking, parsed.max)
	if err := tui.Run(application.Stdin, application.Stdout, model); err != nil {
		return application.fail(fmt.Errorf("interactive interface: %w", err), 1)
	}
	return 0
}

func (application App) runHome(ctx context.Context, current config.Config, formats []string, parsed options) int {
	homeHint := ""
	github := current.Providers["github"]
	if github.Enabled && current.Token() == "" && github.Instance == "" {
		homeHint = "GitHub search is limited. Set GITHUB_TOKEN to search code and more repositories."
	} else if !github.Enabled {
		homeHint = "GitHub search is off. Enable it in the config to search more repositories."
	}
	model := tui.NewHomeModel(func(query string) (<-chan provider.Event, error) {
		events, _, err := application.searchEvents(ctx, current, query, formats, parsed)
		return events, err
	}, application.interactiveDownloader(ctx, parsed), colorEnabled(), "", current.Ranking, parsed.max, homeHint)
	if err := tui.Run(application.Stdin, application.Stdout, model); err != nil {
		return application.fail(fmt.Errorf("interactive interface: %w", err), 1)
	}
	return 0
}

func (application App) interactiveDownloader(ctx context.Context, parsed options) tui.DownloadFunc {
	downloader := download.Downloader{Client: application.Client, AllowInsecure: parsed.allowInsecure}
	health, healthAvailable := urlHealthStore()
	return func(result provider.Result) (string, error) {
		file, err := downloader.Download(ctx, result, expandHome(parsed.downloadDir))
		if err != nil && healthAvailable && download.IsInvalidContent(err) {
			_ = health.MarkInvalidURL(resultHealthKey(result))
		}
		return file.Path, err
	}
}

func selectProviders(available map[string]provider.Provider, current config.Config, requested string) ([]provider.Provider, error) {
	names := make([]string, 0)
	if requested != "" && requested != "all" {
		for _, name := range strings.Split(requested, ",") {
			names = append(names, strings.TrimSpace(strings.ToLower(name)))
		}
	} else {
		for name := range available {
			if setting, exists := current.Providers[name]; !exists || setting.Enabled {
				names = append(names, name)
			}
		}
		sort.Strings(names)
	}
	selected := make([]provider.Provider, 0, len(names))
	for _, name := range names {
		source, exists := available[name]
		if !exists {
			if name == "github" {
				return nil, errors.New("GitHub search isn't configured. Set GITHUB_TOKEN or github_token in the config file, then try again")
			}
			if name == "websearch" {
				return nil, errors.New("web search isn't available. Install kagi-cli or configure a SearXNG instance, then try again")
			}
			return nil, fmt.Errorf("provider %q isn't configured. Enable it in the config file, then try again", name)
		}
		selected = append(selected, source)
	}
	if len(selected) == 0 {
		return nil, errors.New("no search providers are enabled. Enable at least one provider in the config file, then try again")
	}
	return selected, nil
}

func validateProviderNames(value string) error {
	if value == "" || value == "all" {
		return nil
	}
	for _, raw := range strings.Split(value, ",") {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name != "github" && name != "getfonts" && name != "registry" && name != "plugins" && name != "websearch" {
			return fmt.Errorf("unknown provider %q (choose github, getfonts, registry, plugins, or websearch)", name)
		}
	}
	return nil
}
