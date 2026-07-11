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

	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/provider"
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
	configBody := fmt.Sprintf("download_dir: %s\nsearch_timeout_seconds: 2\ncache_ttl_seconds: 60\ndefault_formats: [otf]\nproviders:\n  github:\n    enabled: false\n  getfonts:\n    enabled: true\n    instance: %s\n  registry:\n    enabled: false\n  websearch:\n    enabled: false\n", filepath.Join(root, "fonts"), server.URL)
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

func TestRunGetFallsBackAfterInvalidRankedCandidate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	invalidRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/best.otf" {
			invalidRequests++
			response.Write([]byte("not a font"))
			return
		}
		response.Write(append([]byte("\x00\x01\x00\x00"), make([]byte, 32)...))
	}))
	defer server.Close()
	destination := filepath.Join(root, "fonts")
	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client()}
	results := []provider.Result{
		{URL: server.URL + "/best.otf", Filename: "Example-Bold.otf", Format: "otf", Source: "first"},
		{URL: server.URL + "/fallback.ttf", Filename: "Example-Bold.ttf", Format: "ttf", Source: "second"},
	}

	if code := application.runGet(context.Background(), results, options{max: 1, downloadDir: destination}, false); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(destination, "Example-Bold.ttf")); err != nil {
		t.Fatalf("fallback was not downloaded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "Example-Bold.otf")); !os.IsNotExist(err) {
		t.Fatalf("invalid candidate was saved: %v", err)
	}
	if code := application.runGet(context.Background(), results, options{max: 1, downloadDir: destination}, false); code != 0 {
		t.Fatalf("second code=%d stderr=%s", code, stderr.String())
	}
	if invalidRequests != 1 {
		t.Fatalf("known-invalid URL was requested %d times, want once", invalidRequests)
	}
}

func TestRunGetKeepsCandidatesBeyondRequestedMaximumForFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/invalid/Example-Bold.otf":
			response.Write([]byte("not a font"))
		case "/valid/Example-Bold.ttf":
			response.Write(append([]byte("\x00\x01\x00\x00"), make([]byte, 32)...))
		default:
			fmt.Fprintf(response, `{"items":[{"name":"Example-Bold.otf","html_url":%q,"repository":{"full_name":"bad/fonts"}},{"name":"Example-Bold.ttf","html_url":%q,"repository":{"full_name":"good/fonts"}}]}`, serverURL(request, "/invalid/Example-Bold.otf"), serverURL(request, "/valid/Example-Bold.ttf"))
		}
	}))
	defer server.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	configBody := fmt.Sprintf("download_dir: %s\nsearch_timeout_seconds: 2\ncache_ttl_seconds: 60\ndefault_formats: [otf, ttf]\nproviders:\n  github:\n    enabled: false\n  getfonts:\n    enabled: true\n    instance: %s\n  registry:\n    enabled: false\n  websearch:\n    enabled: false\n", filepath.Join(root, "fonts"), server.URL)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOJI_CONFIG", configPath)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client()}

	if code := application.Run(context.Background(), []string{"get", "Example bold", "--allow-insecure", "--no-cache"}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "fonts", "Example-Bold.ttf")); err != nil {
		t.Fatalf("candidate beyond max=1 was not used: %v", err)
	}
}

func serverURL(request *http.Request, path string) string {
	return "http://" + request.Host + path
}

func TestRunGetReportsEveryFailedCandidate(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte("not a font"))
	}))
	defer server.Close()
	var stderr bytes.Buffer
	application := App{Stdout: &bytes.Buffer{}, Stderr: &stderr, Client: server.Client()}
	results := []provider.Result{
		{URL: server.URL + "/one.otf", Filename: "Example-One.otf", Format: "otf", Source: "one"},
		{URL: server.URL + "/two.ttf", Filename: "Example-Two.ttf", Format: "ttf", Source: "two"},
	}

	if code := application.runGet(context.Background(), results, options{max: 1, downloadDir: t.TempDir()}, false); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Example-One.otf") || !strings.Contains(stderr.String(), "Example-Two.ttf") {
		t.Fatalf("aggregate error omitted a candidate: %s", stderr.String())
	}
}

func TestInteractiveDownloadRecordsInvalidURLHealth(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Write([]byte("not a font"))
	}))
	defer server.Close()
	application := App{Client: server.Client()}
	downloadFont := application.interactiveDownloader(context.Background(), options{downloadDir: filepath.Join(root, "fonts")})
	_, err := downloadFont(provider.Result{URL: server.URL, Filename: "Example.otf", Format: "otf"})
	if err == nil {
		t.Fatal("interactive invalid download error = nil")
	}
	directory, directoryErr := cache.DefaultDirectory()
	if directoryErr != nil {
		t.Fatal(directoryErr)
	}
	invalid, healthErr := (cache.Store{Directory: directory}).IsInvalidURL(server.URL)
	if healthErr != nil || !invalid {
		t.Fatalf("invalid=%v err=%v, want TUI failure recorded", invalid, healthErr)
	}
}

func TestRunGetFamilyFallsBackAsACompleteSameSourceGroup(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/broken/") && strings.Contains(request.URL.Path, "Bold") {
			response.Write([]byte("not a font"))
			return
		}
		response.Write(append(append([]byte("OTTO"), []byte(request.URL.Path)...), make([]byte, 32)...))
	}))
	defer server.Close()
	destination := t.TempDir()
	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client()}
	results := []provider.Result{
		{URL: server.URL + "/broken/Regular.otf", Filename: "Example-Regular.otf", Format: "otf", Source: "broken", Score: 20},
		{URL: server.URL + "/broken/Bold.otf", Filename: "Example-Bold.otf", Format: "otf", Source: "broken", Score: 19},
		{URL: server.URL + "/healthy/Regular.otf", Filename: "Example-Regular.otf", Format: "otf", Source: "healthy", Score: 18},
		{URL: server.URL + "/healthy/Bold.otf", Filename: "Example-Bold.otf", Format: "otf", Source: "healthy", Score: 17},
	}

	if code := application.runGet(context.Background(), results, options{max: 10, downloadDir: destination}, true); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, name := range []string{"Example-Regular.otf", "Example-Bold.otf"} {
		if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
			t.Fatalf("healthy family member %s missing: %v", name, err)
		}
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 2 {
		t.Fatalf("family destination contains partial or staging files: entries=%v err=%v", entries, err)
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
