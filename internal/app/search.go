package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/microck/moji/internal/aggregator"
	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/download"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/tui"
)

func (application App) search(ctx context.Context, current config.Config, query string, formats []string, parsed options) ([]provider.Result, []string, error) {
	events, providerCount, err := application.searchEvents(ctx, current, query, formats, parsed)
	if err != nil {
		return nil, nil, err
	}
	results := make([]provider.Result, 0)
	failures := make([]string, 0)
	started := time.Now()
	for event := range events {
		if event.Type == provider.EventResult {
			results = append(results, event.Result)
		} else if event.Status == provider.StateFailed {
			failures = append(failures, provider.DescribeFailure(event.Provider, event.Err))
		}
		if parsed.debug {
			fmt.Fprintf(application.Stderr, "debug: provider=%s state=%d count=%d retry_after=%s\n", event.Provider, event.Status, event.Count, event.RetryAfter)
		}
	}
	results = provider.UniqueResults(results)
	if parsed.verbose {
		fmt.Fprintf(application.Stderr, "searched %d providers in %s; %d unique results\n", providerCount, time.Since(started).Round(time.Millisecond), len(results))
	}
	if len(results) == 0 && len(failures) == providerCount {
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
	available := map[string]provider.Provider{
		"getfonts": provider.GetFonts{Client: application.Client, Endpoint: current.Providers["getfonts"].Instance},
	}
	githubSettings := current.Providers["github"]
	if token := current.Token(); token != "" || githubSettings.Instance != "" {
		available["github"] = provider.GitHub{Client: application.Client, Endpoint: githubSettings.Instance, Token: token}
	}
	if web := current.Providers["websearch"]; web.Enabled && web.Instance != "" {
		available["websearch"] = provider.WebSearch{Client: application.Client, Instance: web.Instance}
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
	sourceEvents := aggregate.Search(searchCtx, query, formats)
	events := make(chan provider.Event)
	go func() {
		defer close(events)
		defer cancel()
		for event := range sourceEvents {
			events <- event
		}
	}()
	return events, len(selected), nil
}

func (application App) runInteractive(ctx context.Context, current config.Config, query string, formats []string, parsed options) int {
	events, _, err := application.searchEvents(ctx, current, query, formats, parsed)
	if err != nil {
		return application.fail(err, 1)
	}
	downloader := download.Downloader{Client: application.Client, AllowInsecure: parsed.allowInsecure}
	model := tui.NewLiveModel(events, func(result provider.Result) (string, error) {
		file, err := downloader.Download(ctx, result, expandHome(parsed.downloadDir))
		return file.Path, err
	}, colorEnabled(), strings.ToLower(parsed.weight), current.Ranking, parsed.max)
	if err := tui.Run(application.Stdin, application.Stdout, model); err != nil {
		return application.fail(fmt.Errorf("interactive interface: %w", err), 1)
	}
	return 0
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
			return nil, fmt.Errorf("provider %q isn't configured. Enable it in the config file, then try again", name)
		}
		selected = append(selected, source)
	}
	if len(selected) == 0 {
		return nil, errors.New("no search providers are enabled. Enable getfonts in the config file, then try again")
	}
	return selected, nil
}

func validateProviderNames(value string) error {
	if value == "" || value == "all" {
		return nil
	}
	for _, raw := range strings.Split(value, ",") {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name != "github" && name != "getfonts" && name != "websearch" {
			return fmt.Errorf("unknown provider %q (choose github, getfonts, or configured websearch)", name)
		}
	}
	return nil
}
