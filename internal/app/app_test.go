package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSearchJSONAndGetDownload(t *testing.T) {
	font := append([]byte("OTTO"), make([]byte, 32)...)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/font/Example-Bold.otf" {
			response.Write(font)
			return
		}
		fmt.Fprintf(response, "{\"items\":[{\"name\":\"Example-Bold.otf\",\"html_url\":%q,\"repository\":{\"full_name\":\"fixture/fonts\"}}]}", "http://"+request.Host+"/font/Example-Bold.otf")
	}))
	defer server.Close()

	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	configBody := fmt.Sprintf("download_dir: %s\nsearch_timeout_seconds: 2\ncache_ttl_seconds: 60\ndefault_formats: [otf]\nproviders:\n  github:\n    enabled: false\n  getfonts:\n    enabled: true\n    instance: %s\n  websearch:\n    enabled: false\n", filepath.Join(root, "fonts"), server.URL)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOJI_CONFIG", configPath)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))

	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client()}
	if code := application.Run(context.Background(), []string{"Example", "--json", "--no-cache"}); code != 0 {
		t.Fatalf("search code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Example-Bold.otf") || !strings.Contains(stdout.String(), "fixture/fonts") {
		t.Fatalf("search output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := application.Run(context.Background(), []string{"get", "Example bold", "--allow-insecure", "--no-cache"}); code != 0 {
		t.Fatalf("get code=%d stderr=%s", code, stderr.String())
	}
	downloaded := filepath.Join(root, "fonts", "Example-Bold.otf")
	content, err := os.ReadFile(downloaded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, font) || !strings.Contains(stdout.String(), "Downloaded:") {
		t.Fatalf("download output=%s content=%x", stdout.String(), content)
	}
}

func TestRunRejectsUnsupportedProviderAndBadUsage(t *testing.T) {
	t.Setenv("MOJI_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr}
	if code := application.Run(context.Background(), []string{"Inter", "--provider", "getthefont"}); code != 2 {
		t.Fatalf("provider exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown provider \"getthefont\"") {
		t.Fatalf("provider error = %s", stderr.String())
	}
	stderr.Reset()
	if code := application.Run(context.Background(), []string{"Inter", "--format", "exe"}); code != 2 {
		t.Fatalf("usage exit code = %d", code)
	}
}

func TestHelpUsesPlainProductDescription(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &bytes.Buffer{}}).Run(context.Background(), []string{"--help"})
	if code != 0 || !strings.Contains(stdout.String(), "a terminal font finder") || strings.Contains(stdout.String(), "cozy") {
		t.Fatalf("code=%d help=%q", code, stdout.String())
	}
}

func TestConfigShowRedactsToken(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("github_token: ghp_secretvalue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOJI_CONFIG", path)
	var stdout bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if code := application.Run(context.Background(), []string{"config", "show"}); code != 0 {
		t.Fatalf("config show code = %d", code)
	}
	if strings.Contains(stdout.String(), "ghp_secretvalue") || !strings.Contains(stdout.String(), "[redacted]") {
		t.Fatalf("token was not redacted: %s", stdout.String())
	}
}

func TestMissingQueryExplainsHowToRecover(t *testing.T) {
	t.Setenv("MOJI_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	var stderr bytes.Buffer
	code := (App{Stdout: &bytes.Buffer{}, Stderr: &stderr}).Run(context.Background(), []string{"get"})
	if code != 2 || !strings.Contains(stderr.String(), "example: moji \"Inter\"") {
		t.Fatalf("code=%d error=%q", code, stderr.String())
	}
}

func TestBareCommandRequiresInteractiveTerminal(t *testing.T) {
	t.Setenv("MOJI_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	var stdout, stderr bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), nil)
	if code != 2 || !strings.Contains(stderr.String(), "font query is required") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestIsTerminalRejectsNonTTYCharacterDevice(t *testing.T) {
	device, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	if isTerminal(device) {
		t.Fatal("/dev/null must not be treated as an interactive terminal")
	}
}

func TestBareNonInteractiveUsageDoesNotDependOnValidConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOJI_CONFIG", path)
	var stderr bytes.Buffer
	code := (App{Stdout: &bytes.Buffer{}, Stderr: &stderr}).Run(context.Background(), nil)
	if code != 2 || !strings.Contains(stderr.String(), "font query is required") || strings.Contains(stderr.String(), "parse config") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestConfigWithoutEditorProvidesDirectPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	t.Setenv("MOJI_CONFIG", path)
	t.Setenv("EDITOR", "")
	var stderr bytes.Buffer
	code := (App{Stdout: &bytes.Buffer{}, Stderr: &stderr}).Run(context.Background(), []string{"config"})
	if code != 1 || !strings.Contains(stderr.String(), "edit "+path+" directly") {
		t.Fatalf("code=%d error=%q", code, stderr.String())
	}
}

func TestParseEditorCommandPreservesArgumentsAndQuotedPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  []string
	}{
		{"code --wait", []string{"code", "--wait"}},
		{`"/Applications/Visual Studio Code.app/Contents/MacOS/Electron" --wait`, []string{"/Applications/Visual Studio Code.app/Contents/MacOS/Electron", "--wait"}},
		{`"C:\Program Files\Editor\editor.exe" --wait`, []string{`C:\Program Files\Editor\editor.exe`, "--wait"}},
	}
	for _, test := range tests {
		got, err := parseEditorCommand(test.value)
		if err != nil {
			t.Fatalf("parseEditorCommand(%q): %v", test.value, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Errorf("parseEditorCommand(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}
