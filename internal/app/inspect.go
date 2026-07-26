package app

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/microck/moji/internal/fontinspect"
)

type inspectOptions struct {
	input string
	json  bool
}

func (application App) runInspect(args []string) int {
	parsed, err := parseInspectOptions(args)
	if err != nil {
		return application.fail(err, 2)
	}
	inspected, err := fontinspect.Inspect(parsed.input)
	if err != nil {
		if fontinspect.IsUnsupported(err) {
			return application.fail(err, 2)
		}
		return application.fail(err, 1)
	}
	if parsed.json {
		return application.writeJSON(inspected)
	}
	fmt.Fprintf(application.Stdout, "Path: %s\n", escapeTerminalControls(inspected.Path))
	fmt.Fprintf(application.Stdout, "Format: %s\n", escapeTerminalControls(string(inspected.Format)))
	if inspected.Family != "" {
		fmt.Fprintf(application.Stdout, "Family: %s\n", escapeTerminalControls(inspected.Family))
	}
	if inspected.Subfamily != "" {
		fmt.Fprintf(application.Stdout, "Subfamily: %s\n", escapeTerminalControls(inspected.Subfamily))
	}
	fmt.Fprintf(application.Stdout, "Glyphs: %d\n", inspected.Glyphs)
	fmt.Fprintf(application.Stdout, "Unicode: %s\n", inspected.UnicodeVersion)
	fmt.Fprintf(application.Stdout, "Encoded characters: %d\n\n", inspected.EncodedCharacters)

	table := tabwriter.NewWriter(application.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "Script\tEncoded\tAssigned\tCoverage")
	for _, script := range inspected.Scripts {
		fmt.Fprintf(table, "%s\t%d\t%d\t%.2f%%\n", script.Name, script.Encoded, script.Assigned, script.Coverage)
	}
	if err := table.Flush(); err != nil {
		return application.fail(fmt.Errorf("write inspection output: %w", err), 1)
	}
	return 0
}

func escapeTerminalControls(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			fmt.Fprintf(&escaped, "\\u%04X", character)
			continue
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

func parseInspectOptions(args []string) (inspectOptions, error) {
	var parsed inspectOptions
	for _, argument := range args {
		switch argument {
		case "--json":
			parsed.json = true
		default:
			if strings.HasPrefix(argument, "-") {
				return parsed, fmt.Errorf("unknown flag %s", argument)
			}
			if parsed.input != "" {
				return parsed, errors.New("moji inspect accepts exactly one input file")
			}
			parsed.input = argument
		}
	}
	if strings.TrimSpace(parsed.input) == "" {
		return parsed, errors.New("font input is required; example: moji inspect Inter.ttf")
	}
	return parsed, nil
}
