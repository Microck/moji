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
	build := exec.Command("go", "build", "-ldflags=-X=github.com/microck/moji/internal/app.allowPrivateBuild=e2e", "-o", binary, "../cmd/moji")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	font := append([]byte("OTTO"), make([]byte, 64)...)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/font-") {
			response.Write(font)
			return
		}
		switch request.URL.Path {
		case "/api/search":
			items := make([]string, 12)
			for index := range items {
				name := fmt.Sprintf("MojiFixture-%02d-Regular.otf", index)
				if index == 0 {
					name = "MojiFixture-Regular.otf"
				}
				items[index] = fmt.Sprintf("{\"name\":%q,\"html_url\":%q,\"repository\":{\"full_name\":\"fixture/fonts\"}}", name, fmt.Sprintf("http://%s/font-%02d.otf", request.Host, index))
			}
			fmt.Fprintf(response, "{\"items\":[%s]}", strings.Join(items, ","))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	configPath := filepath.Join(root, "config.yaml")
	downloadDirectory := filepath.Join(root, "downloads")
	configBody := fmt.Sprintf("download_dir: %s\nsearch_timeout_seconds: 2\ncache_ttl_seconds: 60\ndefault_formats: [otf]\nproviders:\n  github:\n    enabled: false\n  getfonts:\n    enabled: true\n    instance: %s\n  registry:\n    enabled: false\n  websearch:\n    enabled: false\n", downloadDirectory, server.URL+"/api/search")
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
	if count := bytes.Count(searchOutput, []byte(`"filename"`)); count != 10 {
		t.Fatalf("non-interactive default returned %d results, want 10: %s", count, searchOutput)
	}

	interactiveOutput := runTUI(t, binary, []string{"MojiFixture", "--no-cache"}, environment, nil, "12 options  12 files")
	if !bytes.Contains(interactiveOutput, []byte("12 options  12 files")) || !bytes.Contains(interactiveOutput, []byte("Moji Fixture")) {
		t.Fatalf("TUI did not render fixture result: %q", interactiveOutput)
	}
	homeOutput := runTUI(t, binary, nil, environment, []byte("MojiFixture\r"), "12 options  12 files")
	if !bytes.Contains(homeOutput, []byte("Type a font name")) || !bytes.Contains(homeOutput, []byte("12 options  12 files")) {
		t.Fatalf("home TUI did not transition to results: %q", homeOutput)
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

func runTUI(t *testing.T, binary string, args []string, environment []string, input []byte, readyText string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(environment, "TERM=xterm-256color")
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	output := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	readUntil := func(text string) {
		// Give each sequential phase its own budget. A slow home render should not
		// consume the time reserved for the provider round-trip and results view.
		if err := terminal.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
			t.Fatal(err)
		}
		for !bytes.Contains(output, []byte(text)) {
			read, readErr := terminal.Read(buffer)
			output = append(output, buffer[:read]...)
			if readErr != nil {
				t.Fatalf("TUI did not become ready: %v\n%s", readErr, output)
			}
		}
	}
	if len(input) > 0 {
		readUntil("Type a font name")
		if _, err := terminal.Write(input); err != nil {
			t.Fatal(err)
		}
	}
	readUntil(readyText)
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	remainder, readErr := io.ReadAll(terminal)
	output = append(output, remainder...)
	if readErr != nil && !strings.Contains(readErr.Error(), "input/output error") {
		t.Fatalf("read TUI: %v", readErr)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("TUI exit: %v\n%s", err, output)
	}
	return output
}
