package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxPluginResponseSize int64 = 2 << 20

type PluginSearch struct {
	Client *http.Client
	Paths  []string
}

func (PluginSearch) Name() string { return "plugins" }

type pluginRequest struct {
	Version int      `json:"version"`
	Query   string   `json:"query"`
	Formats []string `json:"formats"`
}

type pluginResponse struct {
	Version int `json:"version"`
	Results []struct {
		URL     string `json:"url"`
		License string `json:"license,omitempty"`
		Trusted bool   `json:"trusted,omitempty"`
	} `json:"results"`
}

func (source PluginSearch) Search(ctx context.Context, query string, formats []string, out chan<- Event) error {
	allowed := formatSet(formats)
	completed := 0
	errorsByPlugin := make([]string, 0)
	for _, path := range source.Paths {
		response, err := runSourcePlugin(ctx, path, pluginRequest{Version: 1, Query: query, Formats: formats})
		if err != nil {
			errorsByPlugin = append(errorsByPlugin, filepath.Base(path)+": "+err.Error())
			continue
		}
		completed++
		for _, candidate := range response.Results {
			results, resolveErr := resolveDiscoveredURL(ctx, source.Client, candidate.URL, query, allowed)
			if resolveErr != nil {
				continue
			}
			for _, result := range results {
				result.Source = "plugin:" + filepath.Base(path)
				result.Trusted = candidate.Trusted
				if candidate.License != "" {
					result.License = candidate.License
				}
				out <- Event{Type: EventResult, Result: result}
			}
		}
	}
	if completed == 0 {
		return fmt.Errorf("%w: %s", ErrUnavailable, strings.Join(errorsByPlugin, "; "))
	}
	return nil
}

func runSourcePlugin(ctx context.Context, path string, request pluginRequest) (pluginResponse, error) {
	input, err := json.Marshal(request)
	if err != nil {
		return pluginResponse{}, err
	}
	command := exec.CommandContext(ctx, path)
	configurePluginCommand(command)
	command.Stdin = bytes.NewReader(input)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return pluginResponse{}, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return pluginResponse{}, err
	}
	content, readErr := io.ReadAll(io.LimitReader(stdout, maxPluginResponseSize+1))
	if int64(len(content)) > maxPluginResponseSize {
		// A plugin may spawn a child that inherits stdout. Close Moji's read end
		// before waiting so that descendant cannot keep the pipe alive after the
		// plugin process is killed.
		_ = stdout.Close()
		_ = terminatePluginCommand(command)
		_ = command.Wait()
		return pluginResponse{}, fmt.Errorf("response exceeds %d bytes", maxPluginResponseSize)
	}
	waitErr := command.Wait()
	if readErr != nil {
		return pluginResponse{}, readErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return pluginResponse{}, fmt.Errorf("%v: %s", waitErr, message)
		}
		return pluginResponse{}, waitErr
	}
	var response pluginResponse
	if err := json.Unmarshal(content, &response); err != nil {
		return pluginResponse{}, fmt.Errorf("invalid JSON response: %w", err)
	}
	if response.Version != 1 {
		return pluginResponse{}, fmt.Errorf("unsupported response version %d", response.Version)
	}
	return response, nil
}
