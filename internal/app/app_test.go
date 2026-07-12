package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/microck/moji/internal/cache"
	"github.com/microck/moji/internal/config"
	"github.com/microck/moji/internal/provider"
)

func TestGitHubCLITokenIsAnEphemeralFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the executable fixture uses a POSIX shell")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "gh")
	fixture := `#!/bin/sh
test "$GH_PROMPT_DISABLED" = "1" || exit 8
test "$1" = "auth" || exit 9
test "$2" = "token" || exit 10
test "$3" = "--hostname" || exit 11
test "$4" = "github.com" || exit 12
printf '  cli-token  '
`
	if err := os.WriteFile(executable, []byte(fixture), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("GITHUB_TOKEN", "")

	current := config.Default()
	resolved := resolveGitHubToken(context.Background(), current, "")
	if resolved.GitHubToken != "cli-token" {
		t.Fatalf("resolved token = %q", resolved.GitHubToken)
	}
	if current.GitHubToken != "" {
		t.Fatalf("CLI token mutated the loaded config: %q", current.GitHubToken)
	}
}

func TestExplicitGitHubTokensPrecedeGitHubCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	current := config.Default()
	current.GitHubToken = "config-token"
	if got := resolveGitHubToken(context.Background(), current, "").GitHubToken; got != "config-token" {
		t.Fatalf("config token = %q", got)
	}

	t.Setenv("GITHUB_TOKEN", "environment-token")
	if got := resolveGitHubToken(context.Background(), current, "").Token(); got != "environment-token" {
		t.Fatalf("environment token = %q", got)
	}
}

func TestGitHubCLITokenRunsOnlyForDefaultSelectedGitHub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the executable fixture uses a POSIX shell")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "called")
	executable := filepath.Join(root, "gh")
	fixture := "#!/bin/sh\nprintf called > '" + marker + "'\nprintf token\n"
	if err := os.WriteFile(executable, []byte(fixture), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("GITHUB_TOKEN", "")

	current := config.Default()
	resolveGitHubToken(context.Background(), current, "getfonts")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("GitHub CLI ran for a getfonts-only search: %v", err)
	}

	current.Providers["github"] = config.ProviderConfig{Enabled: true, Instance: "https://example.test"}
	resolveGitHubToken(context.Background(), current, "github")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("GitHub CLI ran for a custom endpoint: %v", err)
	}
}

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
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client(), allowPrivate: true}
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
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client(), allowPrivate: true}
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
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client(), allowPrivate: true}

	if code := application.Run(context.Background(), []string{"get", "Example bold", "--allow-insecure", "--no-cache"}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "fonts", "Example-Bold.ttf")); err != nil {
		t.Fatalf("candidate beyond max=1 was not used: %v", err)
	}
}

func TestFamilyDryRunPreviewsTheFirstDownloadGroup(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr}
	results := []provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "singleton", Score: 13},
		{Filename: "Example-Regular.ttf", Format: "ttf", Source: "family", Score: 12},
		{Filename: "Example-Bold.ttf", Format: "ttf", Source: "family", Score: 11},
	}

	code := application.runGet(context.Background(), results, options{dryRun: true, max: 10}, true)
	if code != 0 || strings.Contains(stdout.String(), "singleton") || strings.Count(stdout.String(), "family") != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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
	application := App{Stdout: &bytes.Buffer{}, Stderr: &stderr, Client: server.Client(), allowPrivate: true}
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
	application := App{Client: server.Client(), allowPrivate: true}
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

func TestArchiveMemberHealthDoesNotSuppressValidSibling(t *testing.T) {
	t.Parallel()
	store := cache.Store{Directory: t.TempDir()}
	bad := provider.Result{URL: "https://example.test/family.zip", ArchiveMember: "Bad.otf"}
	good := provider.Result{URL: bad.URL, ArchiveMember: "Good.otf"}
	if err := store.MarkInvalidURL(resultHealthKey(bad)); err != nil {
		t.Fatal(err)
	}
	if invalid, err := store.IsInvalidURL(resultHealthKey(good)); err != nil || invalid {
		t.Fatalf("valid sibling invalid=%v err=%v", invalid, err)
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
	application := App{Stdout: &stdout, Stderr: &stderr, Client: server.Client(), allowPrivate: true}
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
	visible := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".moji-") {
			visible++
		}
	}
	if err != nil || visible != 2 {
		t.Fatalf("family destination contains partial or staging files: entries=%v err=%v", entries, err)
	}
}

func TestRunRejectsUnsupportedProviderAndBadUsage(t *testing.T) {
	t.Setenv("MOJI_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	var stdout, stderr bytes.Buffer
	application := App{Stdout: &stdout, Stderr: &stderr}
	if code := application.Run(context.Background(), []string{"Futura", "--provider", "getthefont"}); code != 2 {
		t.Fatalf("provider exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown provider \"getthefont\"") {
		t.Fatalf("provider error = %s", stderr.String())
	}
	stderr.Reset()
	if code := application.Run(context.Background(), []string{"Futura", "--format", "exe"}); code != 2 {
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

func TestRunConvertsLocalFontWithoutLoadingProviderConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "invalid-config.yaml")
	if err := os.WriteFile(configPath, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOJI_CONFIG", configPath)
	input := writeConversionFixture(t, root, "test-ttf.base64", "font.bin")
	output := filepath.Join(root, "converted.woff2")
	var stdout, stderr bytes.Buffer

	code := (App{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{"convert", input, "--output", output, "--json"})
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var converted struct {
		Input        string `json:"input"`
		Output       string `json:"output"`
		SourceFormat string `json:"source_format"`
		TargetFormat string `json:"target_format"`
		Size         int64  `json:"size"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &converted); err != nil {
		t.Fatalf("JSON output = %q: %v", stdout.String(), err)
	}
	content, err := os.ReadFile(output)
	if err != nil || !bytes.HasPrefix(content, []byte("wOF2")) {
		t.Fatalf("output header = %x err=%v", content[:min(4, len(content))], err)
	}
	if converted.Input != input || converted.Output != output || converted.SourceFormat != "ttf" || converted.TargetFormat != "woff2" || converted.Size != int64(len(content)) || len(converted.SHA256) != 64 {
		t.Fatalf("conversion JSON = %#v", converted)
	}
}

func TestRunConvertReportsPlainOutputAndInfersDesktopFlavor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	input := writeConversionFixture(t, root, "test-woff2.base64", "font.webfont")
	var stdout, stderr bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{"convert", input})
	want := strings.TrimSuffix(input, filepath.Ext(input)) + ".ttf"
	if code != 0 || stderr.Len() != 0 || stdout.String() != "Converted: "+want+"\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunConvertMapsUsageAndOperationalFailures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	input := writeConversionFixture(t, root, "test-ttf.base64", "font.ttf")
	for name, args := range map[string][]string{
		"missing input":    {"convert"},
		"unknown flag":     {"convert", input, "--wat"},
		"unknown target":   {"convert", input, "--to", "woff3"},
		"unsupported pair": {"convert", input, "--to", "otf"},
	} {
		name, args := name, args
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := (App{Stdout: &bytes.Buffer{}, Stderr: &stderr}).Run(context.Background(), args); code != 2 || !strings.Contains(stderr.String(), "moji:") {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
	bad := filepath.Join(root, "bad.ttf")
	if err := os.WriteFile(bad, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := (App{Stdout: &bytes.Buffer{}, Stderr: &stderr}).Run(context.Background(), []string{"convert", bad}); code != 1 {
		t.Fatalf("invalid font code=%d stderr=%q", code, stderr.String())
	}
}

func TestConvertHelpIsCommandSpecific(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &bytes.Buffer{}}).Run(context.Background(), []string{"convert", "--help"})
	if code != 0 || !strings.Contains(stdout.String(), "moji convert <input>") || strings.Contains(stdout.String(), "moji cache clear") {
		t.Fatalf("code=%d help=%q", code, stdout.String())
	}
}

func TestParseConvertOptionsAcceptsLongAndEqualsForms(t *testing.T) {
	t.Parallel()
	parsed, err := parseConvertOptions([]string{"font.ttf", "--to=woff2", "--output", "font.webfont", "--json"})
	if err != nil || parsed.input != "font.ttf" || parsed.output != "font.webfont" || parsed.target != "woff2" || !parsed.json {
		t.Fatalf("parsed=%#v err=%v", parsed, err)
	}
	parsed, err = parseConvertOptions([]string{"-o=font.woff2", "font.ttf", "--to", "woff2"})
	if err != nil || parsed.output != "font.woff2" || parsed.input != "font.ttf" || parsed.target != "woff2" {
		t.Fatalf("equals parsed=%#v err=%v", parsed, err)
	}
}

func writeConversionFixture(t *testing.T, directory, fixture, name string) string {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "fontconvert", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTerminalUIOwnsProcessOutputAndRestoresLogger(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	if code := runTerminalUI(func() int {
		log.Print("must not corrupt the alternate screen")
		return 7
	}); code != 7 {
		t.Fatalf("terminal callback returned %d", code)
	}
	if logs.Len() != 0 {
		t.Fatalf("log escaped into terminal output: %q", logs.String())
	}
	log.Print("restored")
	if !strings.Contains(logs.String(), "restored") {
		t.Fatalf("logger output was not restored: %q", logs.String())
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
	if code != 2 || !strings.Contains(stderr.String(), "example: moji \"Futura\"") {
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

func TestConfigRequiresInteractiveTerminal(t *testing.T) {
	t.Setenv("MOJI_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var stderr bytes.Buffer
	code := (App{Stdout: &bytes.Buffer{}, Stderr: &stderr}).Run(context.Background(), []string{"config"})
	if code != 2 || !strings.Contains(stderr.String(), "interactive terminal") || strings.Contains(stderr.String(), "EDITOR") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
