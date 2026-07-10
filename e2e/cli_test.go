package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestMojiBinaryEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess E2E test")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "moji")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../cmd/moji")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	font := append([]byte("OTTO"), make([]byte, 64)...)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/search":
			fmt.Fprintf(response, "{\"items\":[{\"name\":\"MojiFixture-Regular.otf\",\"html_url\":%q,\"repository\":{\"full_name\":\"fixture/fonts\"}}]}", "http://"+request.Host+"/font.otf")
		case "/font.otf":
			response.Write(font)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	configPath := filepath.Join(root, "config.yaml")
	downloadDirectory := filepath.Join(root, "downloads")
	configBody := fmt.Sprintf("download_dir: %s\nsearch_timeout_seconds: 2\ncache_ttl_seconds: 60\ndefault_formats: [otf]\nproviders:\n  github:\n    enabled: false\n  getfonts:\n    enabled: true\n    instance: %s\n  websearch:\n    enabled: false\n", downloadDirectory, server.URL+"/api/search")
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "MOJI_CONFIG="+configPath, "XDG_CACHE_HOME="+filepath.Join(root, "cache"))

	search := exec.Command(binary, "MojiFixture", "--json", "--no-cache")
	search.Env = environment
	searchOutput, err := search.CombinedOutput()
	if err != nil {
		t.Fatalf("search: %v\n%s", err, searchOutput)
	}
	if !bytes.Contains(searchOutput, []byte("MojiFixture-Regular.otf")) {
		t.Fatalf("unexpected search output: %s", searchOutput)
	}

	interactiveContext, cancelInteractive := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelInteractive()
	interactive := exec.CommandContext(interactiveContext, binary, "MojiFixture", "--no-cache")
	interactive.Env = append(environment, "TERM=xterm-256color")
	terminal, err := pty.Start(interactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := terminal.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	interactiveOutput := make([]byte, 0, 4096)
	readBuffer := make([]byte, 4096)
	for !bytes.Contains(interactiveOutput, []byte("MojiFixture-Regular.otf")) {
		read, readErr := terminal.Read(readBuffer)
		interactiveOutput = append(interactiveOutput, readBuffer[:read]...)
		if readErr != nil {
			t.Fatalf("TUI did not become ready: %v\n%s", readErr, interactiveOutput)
		}
	}
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	remainder, readErr := io.ReadAll(terminal)
	interactiveOutput = append(interactiveOutput, remainder...)
	terminal.Close()
	waitErr := interactive.Wait()
	if readErr != nil && !strings.Contains(readErr.Error(), "input/output error") {
		t.Fatalf("read TUI: %v", readErr)
	}
	if waitErr != nil {
		t.Fatalf("TUI exit: %v\n%s", waitErr, interactiveOutput)
	}
	if !bytes.Contains(interactiveOutput, []byte("Found 1 results")) || !bytes.Contains(interactiveOutput, []byte("MojiFixture-Regular.otf")) {
		t.Fatalf("TUI did not render fixture result: %q", interactiveOutput)
	}

	get := exec.Command(binary, "get", "MojiFixture regular", "--allow-insecure", "--no-cache")
	get.Env = environment
	getOutput, err := get.CombinedOutput()
	if err != nil {
		t.Fatalf("get: %v\n%s", err, getOutput)
	}
	if !strings.Contains(string(getOutput), "Downloaded:") {
		t.Fatalf("unexpected get output: %s", getOutput)
	}
	content, err := os.ReadFile(filepath.Join(downloadDirectory, "MojiFixture-Regular.otf"))
	if err != nil || !bytes.Equal(content, font) {
		t.Fatalf("downloaded font mismatch: err=%v bytes=%x", err, content)
	}

	cacheClear := exec.Command(binary, "cache", "clear")
	cacheClear.Env = environment
	if output, err := cacheClear.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("Cleared cache:")) {
		t.Fatalf("cache clear: %v\n%s", err, output)
	}
}
