package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/download"
	"github.com/microck/moji/internal/provider"
)

func (application App) runGet(ctx context.Context, results []provider.Result, parsed options) int {
	if len(results) == 0 {
		return application.fail(errors.New("no fonts matched the query and filters. Try another name or remove --weight or --format"), 1)
	}
	if parsed.dryRun {
		if parsed.json {
			return application.writeJSON(results)
		}
		fmt.Fprintln(application.Stdout, "Would download:")
		application.writeTable(results)
		return 0
	}
	downloader := download.Downloader{Client: application.Client, AllowInsecure: parsed.allowInsecure}
	files := make([]download.File, 0, len(results))
	for _, result := range results {
		file, err := downloader.Download(ctx, result, expandHome(parsed.downloadDir))
		if err != nil {
			return application.fail(fmt.Errorf("%s was not downloaded: %w", result.Filename, err), 1)
		}
		files = append(files, file)
	}
	if parsed.json {
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
	command := exec.Command(editor, path)
	command.Stdin, command.Stdout, command.Stderr = application.Stdin, application.Stdout, application.Stderr
	if err := command.Run(); err != nil {
		return application.fail(fmt.Errorf("the editor couldn't open %s: %w. Your config file was not changed by Moji", path, err), 1)
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
