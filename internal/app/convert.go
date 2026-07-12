package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/microck/moji/internal/fontconvert"
)

type convertOptions struct {
	input, output string
	target        fontconvert.Format
	json          bool
}

func (application App) runConvert(args []string) int {
	parsed, err := parseConvertOptions(args)
	if err != nil {
		return application.fail(err, 2)
	}
	converted, err := fontconvert.Convert(parsed.input, parsed.output, parsed.target)
	if err != nil {
		if fontconvert.IsUnsupported(err) {
			return application.fail(err, 2)
		}
		return application.fail(err, 1)
	}
	if parsed.json {
		return application.writeJSON(converted)
	}
	fmt.Fprintf(application.Stdout, "Converted: %s\n", converted.Output)
	return 0
}

func parseConvertOptions(args []string) (convertOptions, error) {
	var parsed convertOptions
	for index := 0; index < len(args); index++ {
		argument := args[index]
		value := func() (string, error) {
			if equals := strings.IndexByte(argument, '='); equals >= 0 {
				return argument[equals+1:], nil
			}
			index++
			if index >= len(args) {
				return "", fmt.Errorf("%s requires a value", argument)
			}
			return args[index], nil
		}
		name := strings.SplitN(argument, "=", 2)[0]
		switch name {
		case "--to":
			raw, err := value()
			if err != nil {
				return parsed, err
			}
			parsed.target, err = fontconvert.ParseFormat(raw)
			if err != nil {
				return parsed, err
			}
		case "--output", "-o":
			var err error
			parsed.output, err = value()
			if err != nil {
				return parsed, err
			}
			if strings.TrimSpace(parsed.output) == "" {
				return parsed, errors.New("--output requires a non-empty path")
			}
		case "--json":
			parsed.json = true
		default:
			if strings.HasPrefix(argument, "-") {
				return parsed, fmt.Errorf("unknown flag %s", argument)
			}
			if parsed.input != "" {
				return parsed, errors.New("moji convert accepts exactly one input file")
			}
			parsed.input = argument
		}
	}
	if strings.TrimSpace(parsed.input) == "" {
		return parsed, errors.New("font input is required; example: moji convert Inter.ttf")
	}
	return parsed, nil
}
