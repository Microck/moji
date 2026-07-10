package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func parseOptions(args []string) (string, options, error) {
	var parsed options
	queryParts := make([]string, 0)
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
		case "--format", "-f":
			var err error
			parsed.formats, err = value()
			if err != nil {
				return "", parsed, err
			}
		case "--weight", "-w":
			var err error
			parsed.weight, err = value()
			if err != nil {
				return "", parsed, err
			}
		case "--max", "-n":
			raw, err := value()
			if err != nil {
				return "", parsed, err
			}
			parsed.max, err = strconv.Atoi(raw)
			if err != nil || parsed.max < 1 {
				return "", parsed, errors.New("--max must be a positive integer")
			}
		case "--provider":
			var err error
			parsed.providers, err = value()
			if err != nil {
				return "", parsed, err
			}
		case "--download-dir", "-d":
			var err error
			parsed.downloadDir, err = value()
			if err != nil {
				return "", parsed, err
			}
		case "--json":
			parsed.json = true
		case "--dry-run":
			parsed.dryRun = true
		case "--verbose", "-v":
			parsed.verbose = true
		case "--debug":
			parsed.debug = true
		case "--no-cache":
			parsed.noCache = true
		case "--token-stdin":
			parsed.tokenStdin = true
		case "--allow-insecure":
			parsed.allowInsecure = true
		default:
			if strings.HasPrefix(argument, "-") {
				return "", parsed, fmt.Errorf("unknown flag %s", argument)
			}
			queryParts = append(queryParts, argument)
		}
	}
	return strings.TrimSpace(strings.Join(queryParts, " ")), parsed, nil
}
