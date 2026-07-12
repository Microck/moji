package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/download"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
	"github.com/microck/moji/internal/tui"
)

func (application App) runGet(ctx context.Context, results []provider.Result, parsed options, family bool) int {
	if len(results) == 0 {
		return application.fail(errors.New("no fonts matched the query and filters. Try another name or remove --weight or --format"), 1)
	}
	maximum := parsed.max
	if maximum <= 0 {
		maximum = 1
	}
	if parsed.dryRun {
		preview := results
		if family {
			preview = rank.SelectFamily(results, maximum)
		} else if len(preview) > maximum {
			preview = preview[:maximum]
		}
		if parsed.json {
			return application.writeJSON(preview)
		}
		fmt.Fprintln(application.Stdout, "Would download:")
		application.writeTable(preview)
		return 0
	}
	downloader := download.Downloader{Client: application.Client, AllowInsecure: parsed.allowInsecure, AllowPrivate: application.allowPrivate}
	health, healthAvailable := urlHealthStore()
	if family {
		return application.runGetFamily(ctx, downloader, health, healthAvailable, results, maximum, parsed)
	}
	files := make([]download.File, 0, maximum)
	failures := make([]error, 0)
	for _, result := range results {
		if healthAvailable {
			if invalid, err := health.IsInvalidURL(resultHealthKey(result)); err == nil && invalid {
				failures = append(failures, fmt.Errorf("%s from %s: skipped because this URL previously returned invalid font content", result.Filename, result.Source))
				continue
			}
		}
		file, err := downloader.Download(ctx, result, expandHome(parsed.downloadDir))
		if err != nil {
			if healthAvailable && download.IsInvalidContent(err) {
				_ = health.MarkInvalidURL(resultHealthKey(result))
			}
			failures = append(failures, fmt.Errorf("%s from %s: %w", result.Filename, result.Source, err))
			continue
		}
		files = append(files, file)
		if len(files) == maximum {
			break
		}
	}
	if len(files) == 0 {
		return application.fail(fmt.Errorf("none of %d ranked candidates could be downloaded: %w", len(results), errors.Join(failures...)), 1)
	}
	return application.writeDownloadedFiles(files, parsed.json)
}

func (application App) runGetFamily(ctx context.Context, downloader download.Downloader, health cache.Store, healthAvailable bool, results []provider.Result, maximum int, parsed options) int {
	failures := make([]error, 0)
	for _, group := range rank.Groups(results) {
		candidates := group.Files
		if len(candidates) > maximum {
			candidates = candidates[:maximum]
		}
		knownInvalid := ""
		if healthAvailable {
			for _, candidate := range candidates {
				if invalid, err := health.IsInvalidURL(resultHealthKey(candidate)); err == nil && invalid {
					knownInvalid = candidate.Filename
					break
				}
			}
		}
		if knownInvalid != "" {
			failures = append(failures, fmt.Errorf("%s from %s: skipped because %s previously returned invalid font content", group.FamilyName, group.Source, knownInvalid))
			continue
		}
		files, err := downloader.DownloadBatch(ctx, candidates, expandHome(parsed.downloadDir))
		if err == nil {
			return application.writeDownloadedFiles(files, parsed.json)
		}
		if healthAvailable && download.IsInvalidContent(err) {
			_ = health.MarkInvalidURL(download.InvalidContentKey(err))
		}
		failures = append(failures, fmt.Errorf("%s from %s: %w", group.FamilyName, group.Source, err))
	}
	return application.fail(fmt.Errorf("no complete font family could be downloaded: %w", errors.Join(failures...)), 1)
}

func urlHealthStore() (cache.Store, bool) {
	directory, err := cache.DefaultDirectory()
	if err != nil {
		return cache.Store{}, false
	}
	return cache.Store{Directory: directory}, true
}

func resultHealthKey(result provider.Result) string {
	if result.ArchiveMember != "" {
		return result.URL + "\x00" + result.ArchiveMember
	}
	return result.URL
}

func (application App) writeDownloadedFiles(files []download.File, jsonOutput bool) int {
	if jsonOutput {
		return application.writeJSON(files)
	}
	for _, file := range files {
		if file.Existing {
			fmt.Fprintf(application.Stdout, "Already downloaded: %s\n", file.Path)
		} else {
			fmt.Fprintf(application.Stdout, "Downloaded: %s\n", file.Path)
		}
	}
	return 0
}

func (application App) runConfig(current config.Config, path string, args []string) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "show") {
		return application.fail(errors.New("usage: moji config [show]"), 2)
	}
	if len(args) == 1 {
		safe := current
		if safe.GitHubToken != "" {
			safe.GitHubToken = "[redacted]"
		}
		return application.writeJSON(safe)
	}
	if !isTerminal(application.Stdin) || !isTerminal(application.Stdout) {
		return application.fail(errors.New("moji config needs an interactive terminal. Use `moji config show` to inspect the current configuration"), 2)
	}
	if err := tui.RunConfig(application.Stdin, application.Stdout, current, path, colorEnabled()); err != nil {
		return application.fail(fmt.Errorf("configuration interface: %w", err), 1)
	}
	return 0
}

func (application App) runCache(args []string) int {
	if len(args) != 1 || args[0] != "clear" {
		return application.fail(errors.New("usage: moji cache clear"), 2)
	}
	directory, err := cache.DefaultDirectory()
	if err != nil {
		return application.fail(err, 1)
	}
	if err := (cache.Store{Directory: directory}).Clear(); err != nil {
		return application.fail(fmt.Errorf("Moji couldn't clear the cache at %s: %w. Check the directory permissions, then try again", directory, err), 1)
	}
	fmt.Fprintf(application.Stdout, "Cleared cache: %s\n", directory)
	return 0
}
