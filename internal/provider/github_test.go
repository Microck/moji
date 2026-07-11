package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubSearchesOnceAndFiltersFormats(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization was not set")
		}
		query := request.URL.Query().Get("q")
		if !strings.Contains(query, "filename:Example") || !strings.Contains(query, `"Example.otf"`) || strings.Contains(query, "extension:") {
			t.Errorf("filename query = %q", query)
		}
		if got := request.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"items":[{"name":"Example-Bold.otf","path":"fonts/Example-Bold.otf","html_url":"https://github.com/acme/fonts/blob/main/fonts/Example-Bold.otf","repository":{"full_name":"acme/fonts","default_branch":"main"}},{"name":"Example-Regular.TTF","path":"fonts/Example-Regular.TTF","repository":{"full_name":"acme/fonts","default_branch":"main"}},{"name":"Example.woff2","path":"Example.woff2","repository":{"full_name":"acme/fonts","default_branch":"main"}}]}`)
	}))
	defer server.Close()

	out := make(chan Event, 2)
	source := GitHub{Client: server.Client(), Endpoint: server.URL, Token: "secret"}
	if err := source.Search(context.Background(), "Example", []string{"otf", "ttf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 2 {
		t.Fatalf("result count = %d", len(out))
	}
	for event := range out {
		if !strings.HasPrefix(event.Result.URL, "https://raw.githubusercontent.com/acme/fonts/main/fonts/Example-") {
			t.Fatalf("raw URL = %q", event.Result.URL)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestGitHubFallsBackToBroadExtensionQuery(t *testing.T) {
	t.Parallel()
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		queries = append(queries, request.URL.Query().Get("q"))
		if len(queries) == 1 {
			fmt.Fprint(response, `{"items":[]}`)
			return
		}
		fmt.Fprint(response, `{"items":[{"name":"ProximaNova-Regular.ttf","path":"ProximaNova-Regular.ttf","repository":{"full_name":"fixture/fonts","default_branch":"main"}}]}`)
	}))
	defer server.Close()
	out := make(chan Event, 2)
	if err := (GitHub{Client: server.Client(), Endpoint: server.URL}).Search(context.Background(), "Proxima Nova", []string{"ttf"}, out); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[1] != "Proxima Nova extension:ttf" {
		t.Fatalf("queries = %#v", queries)
	}
	if len(out) != 1 {
		t.Fatalf("results = %d", len(out))
	}
}

func TestGitHubFallsBackToRepositoryTreeAndReleaseAssets(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/code":
			fmt.Fprint(response, `{"items":[]}`)
		case "/search/repositories":
			fmt.Fprint(response, `{"items":[{"full_name":"fixture/fonts","default_branch":"main"}]}`)
		case "/repos/fixture/fonts/git/trees/main":
			fmt.Fprint(response, `{"tree":[{"path":"dist/Catedra-Regular.otf","type":"blob","size":1234}]}`)
		case "/repos/fixture/fonts/releases":
			fmt.Fprintf(response, `[{"assets":[{"name":"Catedra-Bold.ttf","browser_download_url":%q,"size":2345}]}]`, server.URL+"/assets/Catedra-Bold.ttf")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 4)
	if err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code"}).Search(context.Background(), "Catedra", []string{"otf", "ttf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 2 {
		t.Fatalf("results = %d, want 2", len(out))
	}
	first := (<-out).Result
	second := (<-out).Result
	if !strings.Contains(first.URL, "raw.githubusercontent.com/fixture/fonts/main/dist/Catedra-Regular.otf") {
		t.Fatalf("tree URL = %q", first.URL)
	}
	if second.URL != server.URL+"/assets/Catedra-Bold.ttf" {
		t.Fatalf("release URL = %q", second.URL)
	}
}

func TestGitHubRepositoryFallbackPropagatesRateLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/code":
			fmt.Fprint(response, `{"items":[]}`)
		case "/search/repositories":
			fmt.Fprint(response, `{"items":[{"full_name":"fixture/fonts","default_branch":"main"}]}`)
		case "/repos/fixture/fonts/git/trees/main":
			response.Header().Set("X-RateLimit-Remaining", "0")
			response.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 2)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code"}).Search(context.Background(), "Example", []string{"otf"}, out)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error = %v, want rate limit", err)
	}
	if len(out) != 1 || (<-out).Status != StateThrottled {
		t.Fatal("rate limit status was not propagated")
	}
}

func TestGitHubRepositoryFallbackChecksReleasesAfterTreeFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/code":
			fmt.Fprint(response, `{"items":[]}`)
		case "/search/repositories":
			fmt.Fprint(response, `{"items":[{"full_name":"fixture/fonts","default_branch":"main"}]}`)
		case "/repos/fixture/fonts/git/trees/main":
			response.WriteHeader(http.StatusInternalServerError)
		case "/repos/fixture/fonts/releases":
			fmt.Fprint(response, `[{"assets":[{"name":"Example.otf","browser_download_url":"https://releases.example/Example.otf","size":123}]}]`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 2)
	if err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code"}).Search(context.Background(), "Example", []string{"otf"}, out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || (<-out).Result.URL != "https://releases.example/Example.otf" {
		t.Fatal("release asset was not checked after tree failure")
	}
}

func TestGitHubFilenameVariants(t *testing.T) {
	t.Parallel()
	query := githubSearchQueries("Proxima Nova", []string{"ttf"})[0]
	for _, want := range []string{"filename:ProximaNova", "filename:Proxima-Nova", "filename:Proxima_Nova", "filename:proximanova", `"ProximaNova.ttf"`} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %q", query, want)
		}
	}
	if len(query) > 240 {
		t.Fatalf("query length = %d", len(query))
	}
}

func TestGitHubRawURLEscapesFilenameSpaces(t *testing.T) {
	got := githubRepositoryRawURL("fixture/fonts", "main", "Family/Example Regular.otf")
	if got != "https://raw.githubusercontent.com/fixture/fonts/main/Family/Example%20Regular.otf" {
		t.Fatalf("URL = %q", got)
	}
}

func TestGitHubRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"seconds", http.Header{"Retry-After": {"7"}}, 7 * time.Second},
		{"date", http.Header{"Retry-After": {now.Add(9 * time.Second).Format(http.TimeFormat)}}, 9 * time.Second},
		{"reset", http.Header{"X-Ratelimit-Reset": {fmt.Sprint(now.Add(11 * time.Second).Unix())}}, 11 * time.Second},
		{"retry after wins", http.Header{"Retry-After": {"7"}, "X-Ratelimit-Reset": {fmt.Sprint(now.Add(11 * time.Second).Unix())}}, 7 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := githubRetryAfter(test.header, now); got != test.want {
				t.Fatalf("retry after = %s, want %s", got, test.want)
			}
		})
	}
}

func TestGetFontsFiltersFormats(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, "{\"items\":[{\"name\":\"Example.otf\",\"html_url\":\"https://github.com/acme/fonts/raw/main/Example.otf\",\"repository\":{\"full_name\":\"acme/fonts\"}},{\"name\":\"Example.eot\",\"html_url\":\"https://example.test/Example.eot\",\"repository\":{\"full_name\":\"acme/fonts\"}}]}")
	}))
	defer server.Close()

	out := make(chan Event, 2)
	if err := (GetFonts{Client: server.Client(), Endpoint: server.URL}).Search(context.Background(), "Example", []string{"otf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 || (<-out).Result.Format != "otf" {
		t.Fatal("format filtering did not preserve exactly the OTF result")
	}
}

func TestWebSearchOnlyReturnsDirectFontLinks(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var release sync.Once
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 6 {
			release.Do(func() { close(ready) })
		}
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Error("expanded web searches did not run concurrently")
		}
		fmt.Fprint(response, "{\"results\":[{\"url\":\"https://cdn.test/Example.woff2\",\"title\":\"font\"},{\"url\":\"https://site.test/page\",\"title\":\"page\"}]}")
	}))
	defer server.Close()

	out := make(chan Event, 2)
	if err := (WebSearch{Client: server.Client(), Instance: server.URL}).Search(context.Background(), "Example", []string{"woff2"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 || (<-out).Result.Filename != "Example.woff2" {
		t.Fatal("direct font result was not extracted")
	}
	if requests.Load() != 9 {
		t.Fatalf("requests = %d, want 9", requests.Load())
	}
}

func TestWebSearchPreservesEveryArchiveMember(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"Example-Regular.otf", "Example-Bold.otf"} {
		font, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := font.Write([]byte("OTTOfont")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/family.zip" {
			_, _ = response.Write(archive.Bytes())
			return
		}
		fmt.Fprintf(response, `{"results":[{"url":%q}]}`, server.URL+"/family.zip")
	}))
	defer server.Close()

	out := make(chan Event, 12)
	if err := (WebSearch{Client: server.Client(), Instance: server.URL}).Search(localDiscoveryContext(), "Example", []string{"otf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 2 {
		t.Fatalf("results = %d, want both archive members", len(out))
	}
}

func TestWebSearchQueriesCoverDorkVariants(t *testing.T) {
	t.Parallel()
	queries := webSearchQueries("Proxima Nova", []string{"otf", "ttf", "dfont"})
	want := []string{
		`"Proxima Nova.ttf"`,
		`"Proxima Nova.otf"`,
		`site:vk.com "Proxima Nova"`,
		`"Proxima Nova" "index of" .otf OR .ttf OR .dfont OR .zip OR .tar.gz`,
		`intitle:"Proxima Nova" github`,
		`"Proxima Nova" "@font-face" filetype:css`,
		`("Proxima Nova.ttf" OR "Proxima Nova.otf") (site:onlinewebfonts.com OR site:wfonts.com OR site:befonts.com OR site:cufonfonts.com)`,
		`("Proxima Nova.ttf" OR "Proxima Nova.otf") (site:freefontsfamily.org OR site:fontsfree.net OR site:dfonts.org OR site:font.download)`,
		`("Proxima Nova.ttf" OR "Proxima Nova.otf") (site:ffonts.net OR site:dafontfree.co OR site:fontshub.pro OR site:fontbolt.com)`,
	}
	if len(queries) != len(want) {
		t.Fatalf("queries = %#v", queries)
	}
	for index := range want {
		if queries[index] != want[index] {
			t.Errorf("query %d = %q, want %q", index, queries[index], want[index])
		}
	}
}

func TestGetFontsLive(t *testing.T) {
	if os.Getenv("MOJI_LIVE_TESTS") != "1" {
		t.Skip("set MOJI_LIVE_TESTS=1 to exercise getfonts.cc")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out := make(chan Event, 100)
	done := make(chan error, 1)
	go func() { done <- (GetFonts{}).Search(ctx, "Inter", []string{"otf"}, out) }()
	results := 0
	for {
		select {
		case event := <-out:
			if event.Type == EventResult {
				results++
			}
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if results == 0 {
				t.Fatal("live GetFonts search returned no OTF results")
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
