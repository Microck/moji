package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPluginSearchAcceptsOnlyDirectConfiguredFormats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable")
	}
	path := filepath.Join(t.TempDir(), "fixture-plugin")
	script := `#!/bin/sh
read request
printf '%s' '{"version":1,"results":[{"url":"https://cdn.example/SilkaMono-Regular.otf","license":"fixture"},{"url":"https://example.com/font-page"}]}'
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 2)
	if err := (PluginSearch{Paths: []string{path}}).Search(context.Background(), "Silka Mono", []string{"otf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 {
		t.Fatalf("results = %d, want 1", len(out))
	}
	result := (<-out).Result
	if result.Source != "plugin:fixture-plugin" || result.License != "fixture" || result.Format != "otf" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPluginSearchRejectsUnknownProtocolVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable")
	}
	path := filepath.Join(t.TempDir(), "future-plugin")
	script := `#!/bin/sh
read request
printf '%s' '{"version":2,"results":[]}'
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (PluginSearch{Paths: []string{path}}).Search(context.Background(), "Example", []string{"otf"}, make(chan Event, 1)); err == nil {
		t.Fatal("expected unsupported protocol version error")
	}
}

func TestPluginOutputLimitStopsNonClosingProcessImmediately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX head and sleep")
	}
	path := filepath.Join(t.TempDir(), "oversized-plugin")
	script := `#!/bin/sh
head -c 2097153 /dev/zero
sleep 5
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := runSourcePlugin(context.Background(), path, pluginRequest{Version: 1, Query: "Example", Formats: []string{"otf"}})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("output limit took %s; process was not stopped immediately", elapsed)
	}
}
