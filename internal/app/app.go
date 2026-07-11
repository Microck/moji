package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/rank"
	"golang.org/x/term"
)

var Version = "0.2.2"
var ReleaseMarker = "moji-release-version:development:moji-marker-end"

// allowPrivateBuild is set only on the subprocess binary built by the E2E
// test. Release builds leave it empty, and no runtime input can change it.
var allowPrivateBuild string

type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Client *http.Client

	allowPrivate bool
}

type options struct {
	formats, weight, providers, downloadDir                          string
	max                                                              int
	json, dryRun, verbose, debug, noCache, tokenStdin, allowInsecure bool
}

func (application App) Run(ctx context.Context, args []string) int {
	runtime.KeepAlive(ReleaseMarker)
	application.allowPrivate = application.allowPrivate || allowPrivateBuild == "e2e"
	application.setDefaults()
	if containsHelp(args) {
		fmt.Fprint(application.Stdout, helpText)
		return 0
	}
	if len(args) > 0 && args[0] == "--version" {
		fmt.Fprintln(application.Stdout, Version)
		return 0
	}
	if len(args) == 0 && (!isTerminal(application.Stdin) || !isTerminal(application.Stdout)) {
		return application.fail(errors.New("font query is required in non-interactive mode; example: moji \"Futura\""), 2)
	}
	configPath, err := config.Path()
	if err != nil {
		return application.fail(err, 1)
	}
	current, err := config.Load(configPath)
	if err != nil {
		return application.fail(err, 1)
	}
	if len(args) == 0 {
		return application.runHome(ctx, current, current.DefaultFormats, options{downloadDir: current.DownloadDir})
	}
	if args[0] == "config" {
		return application.runConfig(current, configPath, args[1:])
	}
	if args[0] == "cache" {
		return application.runCache(args[1:])
	}

	getMode := args[0] == "get"
	if getMode {
		args = args[1:]
	}
	query, parsed, err := parseOptions(args)
	if err != nil {
		return application.fail(err, 2)
	}
	if query == "" {
		return application.fail(errors.New("font query is required; example: moji \"Futura\""), 2)
	}
	if err := validateProviderNames(parsed.providers); err != nil {
		return application.fail(err, 2)
	}
	intent := rank.Intent{Query: query, Max: 1}
	if getMode {
		intent = rank.ParseIntent(query)
		query = intent.Query
		if parsed.weight == "" {
			parsed.weight = intent.WantWeight
		}
		if parsed.formats == "" && intent.Format != "" {
			parsed.formats = intent.Format
		}
	}
	if parsed.dryRun && !getMode {
		return application.fail(errors.New("--dry-run is only valid with moji get"), 2)
	}
	if parsed.tokenStdin {
		token, readErr := io.ReadAll(io.LimitReader(application.Stdin, 4097))
		if readErr != nil {
			return application.fail(fmt.Errorf("read token from stdin: %w", readErr), 1)
		}
		if len(token) > 4096 {
			return application.fail(errors.New("token from stdin exceeds 4096 bytes"), 2)
		}
		current.GitHubToken = strings.TrimSpace(string(token))
	}
	formats := current.DefaultFormats
	if parsed.formats != "" {
		formats, err = config.ParseFormats(parsed.formats)
		if err != nil {
			return application.fail(err, 2)
		}
	}
	if parsed.downloadDir == "" {
		parsed.downloadDir = current.DownloadDir
	}
	interactive := !getMode && !parsed.json && isTerminal(application.Stdin) && isTerminal(application.Stdout)
	if parsed.max == 0 {
		if getMode {
			parsed.max = intent.Max
		} else if !interactive {
			parsed.max = 10
		}
	}
	if interactive {
		return application.runInteractive(ctx, current, query, formats, parsed)
	}
	results, failures, err := application.search(ctx, current, query, formats, parsed)
	if err != nil {
		return application.fail(err, 1)
	}
	if parsed.weight != "" {
		results = rank.FilterWeight(results, strings.ToLower(parsed.weight))
	}
	results = rank.Results(results, query, strings.ToLower(parsed.weight), current.Ranking)
	if !getMode && len(results) > parsed.max {
		results = results[:parsed.max]
	}
	if parsed.verbose {
		for _, failure := range failures {
			fmt.Fprintln(application.Stderr, failure)
		}
	}
	if getMode {
		return application.runGet(ctx, results, parsed, intent.WantFamily)
	}
	if parsed.json {
		return application.writeJSON(results)
	}
	application.writeTable(results)
	return 0
}

func containsHelp(args []string) bool {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" || argument == "help" {
			return true
		}
	}
	return false
}

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func (application *App) setDefaults() {
	if application.Stdin == nil {
		application.Stdin = os.Stdin
	}
	if application.Stdout == nil {
		application.Stdout = os.Stdout
	}
	if application.Stderr == nil {
		application.Stderr = os.Stderr
	}
}
