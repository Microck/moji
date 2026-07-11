package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
)

func (application App) writeJSON(value any) int {
	encoder := json.NewEncoder(application.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return application.fail(err, 1)
	}
	return 0
}

func (application App) writeTable(results []provider.Result) {
	if len(results) == 0 {
		fmt.Fprintln(application.Stdout, "No fonts found.")
		return
	}
	writer := tabwriter.NewWriter(application.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tFORMAT\tWEIGHT\tSOURCE\tSIZE\tLICENSE")
	for index, result := range results {
		weight := rank.WeightOf(result)
		if weight == "" {
			weight = "-"
		}
		format := strings.ToUpper(result.Format)
		if result.Variable {
			format += "-VAR"
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\n", index+1, format, weight, result.Source, formatSize(result.SizeBytes), result.License)
	}
	writer.Flush()
}

func formatSize(bytes int64) string {
	if bytes <= 0 {
		return "-"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	return fmt.Sprintf("%.1fK", float64(bytes)/1024)
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func (application App) fail(err error, code int) int {
	fmt.Fprintf(application.Stderr, "moji: %v\n", redact(err.Error()))
	return code
}

func redact(message string) string {
	for _, prefix := range []string{"ghp_", "github_pat_"} {
		for {
			index := strings.Index(message, prefix)
			if index < 0 {
				break
			}
			end := index
			for end < len(message) && message[end] != ' ' && message[end] != '\n' && message[end] != '\t' {
				end++
			}
			message = message[:index] + "[redacted]" + message[end:]
		}
	}
	return message
}

var helpText = `文字  moji - a terminal font finder

Usage:
  moji                                 Open the interactive font finder
  moji <query> [flags]                 Search and print ranked results
  moji get <query> [flags]             Download the best matching font
  moji config [show]                   Edit or display configuration
  moji cache clear                     Clear cached search results
  moji --version

Examples:
  moji
  moji "Futura"
  moji "Futura" --format otf,ttf --json
  moji get "Futura bold" --dry-run
  moji get "Futura bold" --download-dir ~/Downloads/moji

Flags:
  -f, --format <list>                  otf, ttf, woff, woff2, dfont, pfb, pfm
  -w, --weight <name>                  Filter by font weight
  -n, --max <count>                    Maximum results or downloads
--provider <list>                github,getfonts,registry,plugins,websearch
      --json                           Machine-readable output
      --dry-run                        Preview get downloads
  -d, --download-dir <path>            Download destination
  -v, --verbose                        Provider counts, failures, and timing
      --debug                          Retry and provider-state details
      --no-cache                       Bypass the result cache
      --token-stdin                    Read GitHub token from stdin
      --allow-insecure                 Permit explicitly selected HTTP URLs
  -h, --help                           Show help
`
