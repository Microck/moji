package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/download"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
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
	downloader := download.Downloader{Client: application.Client, AllowInsecure: parsed.allowInsecure}
	health, healthAvailable := urlHealthStore()
	if family {
		return application.runGetFamily(ctx, downloader, health, healthAvailable, results, maximum, parsed)
	}
	files := make([]download.File, 0, maximum)
	failures := make([]error, 0)
	for _, result := range results {
		if healthAvailable {
			if invalid, err := health.IsInvalidURL(result.URL); err == nil && invalid {
				failures = append(failures, fmt.Errorf("%s from %s: skipped because this URL previously returned invalid font content", result.Filename, result.Source))
				continue
			}
		}
		file, err := downloader.Download(ctx, result, expandHome(parsed.downloadDir))
		if err != nil {
			if healthAvailable && download.IsInvalidContent(err) {
				_ = health.MarkInvalidURL(result.URL)
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
				if invalid, err := health.IsInvalidURL(candidate.URL); err == nil && invalid {
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
			_ = health.MarkInvalidURL(download.InvalidContentURL(err))
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
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := config.Save(path, current); err != nil {
			return application.fail(err, 1)
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return application.fail(fmt.Errorf("Moji couldn't open the config because $EDITOR is not set. Set EDITOR to your preferred editor or edit %s directly", path), 1)
	}
	editorCommand, err := parseEditorCommand(editor)
	if err != nil {
		return application.fail(fmt.Errorf("Moji couldn't parse $EDITOR: %w. Set EDITOR to an executable with optional shell-style arguments or edit %s directly", err, path), 1)
	}
	command := exec.Command(editorCommand[0], append(editorCommand[1:], path)...)
	command.Stdin, command.Stdout, command.Stderr = application.Stdin, application.Stdout, application.Stderr
	if err := command.Run(); err != nil {
		return application.fail(fmt.Errorf("the editor couldn't open %s: %w. Your config file was not changed by Moji", path, err), 1)
	}
	return 0
}

func parseEditorCommand(value string) ([]string, error) {
	var command []string
	var current strings.Builder
	var quote rune
	tokenStarted := false
	runes := []rune(value)
	flush := func() {
		if tokenStarted {
			command = append(command, current.String())
			current.Reset()
			tokenStarted = false
		}
	}

	for index := 0; index < len(runes); index++ {
		character := runes[index]
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			// Within double quotes, only escape a quote or another backslash.
			// This preserves Windows paths such as C:\Program Files\Editor.
			if character == '\\' && quote == '"' && index+1 < len(runes) && (runes[index+1] == '"' || runes[index+1] == '\\') {
				index++
				character = runes[index]
			}
			current.WriteRune(character)
			tokenStarted = true
			continue
		}

		switch {
		case character == '\'' || character == '"':
			quote = character
			tokenStarted = true
		case unicode.IsSpace(character):
			flush()
		case character == '\\' && index+1 < len(runes) && (unicode.IsSpace(runes[index+1]) || runes[index+1] == '\'' || runes[index+1] == '"' || runes[index+1] == '\\'):
			index++
			current.WriteRune(runes[index])
			tokenStarted = true
		default:
			current.WriteRune(character)
			tokenStarted = true
		}
	}
	if quote != 0 {
		return nil, errors.New("the value contains an unterminated quote")
	}
	flush()
	if len(command) == 0 {
		return nil, errors.New("the value is empty")
	}
	return command, nil
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
