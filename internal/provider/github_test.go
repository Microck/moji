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
	"strconv"
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
		if request.URL.Path == "/repos/acme/fonts/git/trees/main" {
			fmt.Fprint(response, `{"tree":[{"path":"fonts/Example-Bold.otf","type":"blob"},{"path":"fonts/Example-Regular.TTF","type":"blob"}]}`)
			return
		}
		query := request.URL.Query().Get("q")
		if query != "Example.ttf" && query != "Example.otf" && query != "Example .ttf" && query != "Example .otf" {
			t.Errorf("context query = %q", query)
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
	if requests != 5 {
		t.Fatalf("requests = %d, want four Code Search requests and one family tree", requests)
	}
}

func TestGitHubUsesExactThenContextualSearchBeforeRepositoryFallback(t *testing.T) {
	t.Parallel()
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/code":
			queries = append(queries, request.URL.Query().Get("q"))
			fmt.Fprint(response, `{"items":[]}`)
		case "/search/repositories":
			fmt.Fprint(response, `{"items":[{"full_name":"fixture/fonts","default_branch":"main"}]}`)
		case "/repos/fixture/fonts/git/trees/main":
			fmt.Fprint(response, `{"tree":[{"path":"ProximaNova-Regular.ttf","type":"blob","size":1234}]}`)
		case "/repos/fixture/fonts/releases":
			fmt.Fprint(response, `[]`)
		default:
			t.Fatalf("unexpected request %q", request.URL.Path)
		}
	}))
	defer server.Close()
	out := make(chan Event, 2)
	if err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code"}).Search(context.Background(), "Proxima Nova", []string{"ttf"}, out); err != nil {
		t.Fatal(err)
	}
	wantQueries := []string{"Proxima Nova.ttf", "Proxima Nova .ttf", "Proxima-Nova.ttf", "Proxima_Nova.ttf", "ProximaNova.ttf"}
	if fmt.Sprint(queries) != fmt.Sprint(wantQueries) {
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
	queries := githubSearchQueries("Proxima Nova", []string{"otf", "ttf"})
	want := []string{
		"Proxima Nova.ttf", "Proxima Nova.otf", "Proxima Nova .ttf", "Proxima Nova .otf",
		"Proxima-Nova.ttf", "Proxima-Nova.otf", "Proxima_Nova.ttf", "Proxima_Nova.otf",
		"ProximaNova.ttf", "ProximaNova.otf",
	}
	if fmt.Sprint(queries) != fmt.Sprint(want) {
		t.Fatalf("queries = %#v", queries)
	}
	for _, query := range queries {
		if strings.Contains(query, " OR ") {
			t.Fatalf("REST Code Search query contains unsupported OR fan-out: %q", query)
		}
	}
}

func TestGitHubFilenameVariantsStayWithinCodeSearchRateBudget(t *testing.T) {
	t.Parallel()
	queries := githubSearchQueries("Times New Roman", []string{"otf", "ttf", "woff2", "dfont", "pfb", "pfm"})
	if len(queries) > maxGitHubCodeQueries {
		t.Fatalf("queries = %d, budget = %d", len(queries), maxGitHubCodeQueries)
	}
	joined := strings.Join(queries, "\n")
	for _, required := range []string{
		"Times New Roman.ttf", "Times New Roman.otf", "Times-New-Roman.ttf", "Times-New-Roman.otf",
		"Times_New_Roman.ttf", "Times_New_Roman.otf", "TimesNewRoman.ttf", "TimesNewRoman.otf",
		"Times New Roman .ttf", "Times New Roman .otf",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("queries do not contain %q: %#v", required, queries)
		}
	}
}

func TestGitHubUsesReportedCodeSearchAllowance(t *testing.T) {
	t.Parallel()
	codeCalls := 0
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/search/code" {
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
		codeCalls++
		queries = append(queries, request.URL.Query().Get("q"))
		response.Header().Set("X-RateLimit-Remaining", strconv.Itoa(3-codeCalls))
		fmt.Fprint(response, `{"items":[{"name":"Example.ttf","path":"Example.ttf","repository":{}}]}`)
	}))
	defer server.Close()

	out := make(chan Event, 2)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Example Family", []string{"ttf", "otf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	if codeCalls != 3 {
		t.Fatalf("code calls = %d, want reported allowance", codeCalls)
	}
	if queries[2] != "Example Family .ttf" {
		t.Fatalf("queries = %#v, contextual search did not run before exhaustion", queries)
	}
}

func TestGitHubContextMatchInspectsRepositoryTree(t *testing.T) {
	t.Parallel()
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		switch request.URL.Path {
		case "/search/code":
			if got := request.URL.Query().Get("q"); got != "Garamond Premier Pro.ttf" {
				t.Fatalf("query = %q", got)
			}
			fmt.Fprint(response, `{"items":[{"name":"stylesheet.css","path":"Garamond-Premier-Pro/stylesheet.css","repository":{"full_name":"fixture/fonts","default_branch":"main"}}]}`)
		case "/repos/fixture/fonts/git/trees/main":
			fmt.Fprint(response, `{"tree":[{"path":"Garamond-Premier-Pro/GaramondPremrPro.ttf","type":"blob","size":1000},{"path":"Garamond-Premier-Pro/GaramondPremrPro-Bd.ttf","type":"blob","size":1100},{"path":"Other/Unrelated.ttf","type":"blob","size":1200}]}`)
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	out := make(chan Event, 4)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Garamond Premier Pro", []string{"otf", "ttf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 2 {
		t.Fatalf("results = %d, want two matching direct fonts", len(out))
	}
	for event := range out {
		if !strings.Contains(event.Result.URL, "raw.githubusercontent.com/fixture/fonts/main/Garamond-Premier-Pro/GaramondPremrPro") {
			t.Fatalf("URL = %q", event.Result.URL)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestGitHubExactFilenameContextFindsArcherFont(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/code":
			if got := request.URL.Query().Get("q"); got != "Archer.ttf" {
				t.Fatalf("query = %q", got)
			}
			fmt.Fprint(response, `{"items":[{"name":"App.vue","path":"src/App.vue","repository":{"full_name":"fixture/daegmael","default_branch":"master"}}]}`)
		case "/repos/fixture/daegmael/git/trees/master":
			fmt.Fprint(response, `{"tree":[{"path":"src/assets/fonts/archer.ttf","type":"blob","size":1234}]}`)
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	out := make(chan Event, 2)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Archer", []string{"ttf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 || (<-out).Result.Filename != "archer.ttf" {
		t.Fatal("exact filename context did not find Archer font")
	}
}

func TestGitHubSearchCycleSpendsCodeSearchBudgetOnOneQuerySpelling(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		fmt.Fprint(response, `{"items":[{"name":"Example.ttf","path":"Example.ttf","repository":{"full_name":"fixture/fonts","default_branch":"main"}}]}`)
	}))
	defer server.Close()

	ctx := WithSearchCycle(context.Background())
	for index, query := range []string{"Example", "Example-Regular"} {
		out := make(chan Event, 2)
		err := (GitHub{Client: server.Client(), Endpoint: server.URL, Token: "secret"}).Search(ctx, query, []string{"ttf"}, out)
		if index == 0 && err != nil {
			t.Fatal(err)
		}
		if index == 1 && !errors.Is(err, ErrSearchSkipped) {
			t.Fatalf("adaptive search error = %v, want skipped signal", err)
		}
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want one exact/contextual pair and one family tree per command", requests)
	}
}

func TestGitHubContextStillInspectsSourcesAlongsideDuplicateDirectMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/code":
			if request.URL.Query().Get("q") == "Example.ttf" {
				fmt.Fprint(response, `{"items":[{"name":"Example.ttf","path":"fonts/Example.ttf","repository":{"full_name":"fixture/direct","default_branch":"main"}}]}`)
				return
			}
			fmt.Fprint(response, `{"items":[{"name":"Example.ttf","path":"fonts/Example.ttf","repository":{"full_name":"fixture/direct","default_branch":"main"}},{"name":"stylesheet.css","path":"styles/stylesheet.css","repository":{"full_name":"fixture/context","default_branch":"main"}}]}`)
		case "/repos/fixture/context/git/trees/main":
			fmt.Fprint(response, `{"tree":[{"path":"fonts/Example-Bold.ttf","type":"blob","size":1400}]}`)
		case "/repos/fixture/direct/git/trees/main":
			fmt.Fprint(response, `{"tree":[{"path":"fonts/Example.ttf","type":"blob","size":1200}]}`)
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	out := make(chan Event, 3)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Example", []string{"ttf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 2 {
		t.Fatalf("results = %d, want deduplicated direct match plus contextual tree match", len(out))
	}
	filenames := map[string]bool{}
	for event := range out {
		filenames[event.Result.Filename] = true
	}
	if !filenames["Example.ttf"] || !filenames["Example-Bold.ttf"] {
		t.Fatalf("filenames = %#v", filenames)
	}
}

func TestGitHubRepositoryInspectionBudgetIsSharedAcrossQueries(t *testing.T) {
	t.Parallel()
	codeCalls := 0
	treeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/search/code" {
			codeCalls++
			fmt.Fprintf(response, `{"items":[{"name":"Example.ttf","path":"fonts/Example-%d-a.ttf","repository":{"full_name":"fixture/%d-a","default_branch":"main"}},{"name":"Example.ttf","path":"fonts/Example-%d-b.ttf","repository":{"full_name":"fixture/%d-b","default_branch":"main"}},{"name":"Example.ttf","path":"fonts/Example-%d-c.ttf","repository":{"full_name":"fixture/%d-c","default_branch":"main"}}]}`,
				codeCalls, codeCalls, codeCalls, codeCalls, codeCalls, codeCalls)
			return
		}
		if strings.Contains(request.URL.Path, "/git/trees/main") {
			treeCalls++
			fmt.Fprint(response, `{"tree":[]}`)
			return
		}
		t.Fatalf("unexpected request path %q", request.URL.Path)
	}))
	defer server.Close()

	out := make(chan Event, 12)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Example", []string{"ttf", "otf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	if codeCalls != len(githubSearchQueries("Example", []string{"ttf", "otf"})) || treeCalls > maxGitHubContextRepositories {
		t.Fatalf("code calls = %d, tree calls = %d", codeCalls, treeCalls)
	}
}

func TestGitHubReservesTreeBudgetForLaterContextSources(t *testing.T) {
	t.Parallel()
	directTreeCalls := 0
	contextTreeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/search/code" && request.URL.Query().Get("q") == "Example.ttf":
			fmt.Fprint(response, `{"items":[{"name":"Example.ttf","path":"fonts/Example-a.ttf","repository":{"full_name":"fixture/a","default_branch":"main"}},{"name":"Example.ttf","path":"fonts/Example-b.ttf","repository":{"full_name":"fixture/b","default_branch":"main"}},{"name":"Example.ttf","path":"fonts/Example-c.ttf","repository":{"full_name":"fixture/c","default_branch":"main"}}]}`)
		case request.URL.Path == "/search/code" && request.URL.Query().Get("q") == "Example.otf":
			fmt.Fprint(response, `{"items":[]}`)
		case request.URL.Path == "/search/code":
			fmt.Fprint(response, `{"items":[{"name":"stylesheet.css","path":"Example/stylesheet.css","repository":{"full_name":"fixture/context","default_branch":"main"}}]}`)
		case request.URL.Path == "/repos/fixture/context/git/trees/main":
			contextTreeCalls++
			fmt.Fprint(response, `{"tree":[{"path":"Example/Example-Bold.ttf","type":"blob","size":1400}]}`)
		case strings.Contains(request.URL.Path, "/git/trees/main"):
			directTreeCalls++
			fmt.Fprint(response, `{"tree":[]}`)
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	out := make(chan Event, 6)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Example", []string{"ttf", "otf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	if directTreeCalls != maxGitHubDirectRepositories || contextTreeCalls != 1 {
		t.Fatalf("direct tree calls = %d, context tree calls = %d", directTreeCalls, contextTreeCalls)
	}
	foundSibling := false
	for len(out) > 0 {
		if (<-out).Result.Filename == "Example-Bold.ttf" {
			foundSibling = true
		}
	}
	if !foundSibling {
		t.Fatal("later contextual source did not recover its family sibling")
	}
}

func TestGitHubPathMatchingCombinesFoundryAliases(t *testing.T) {
	t.Parallel()
	for pathValue, query := range map[string]string{
		"BelariusSerifNrRg.ttf":       "Belarius Serif Narrow Regular",
		"GaramondPremrPro-Bd.ttf":     "Garamond Premier Pro Bold",
		"Family/CondLtIt/Example.otf": "Family Condensed Light Italic",
		"Times New Roman.ttf":         "Times New Roman",
		"Times-New-Roman.otf":         "Times New Roman",
		"Times_New_Roman.ttf":         "Times New Roman",
		"TimesNewRoman.otf":           "Times New Roman",
	} {
		if !githubPathMatchesQuery(pathValue, query) {
			t.Errorf("path %q did not match %q", pathValue, query)
		}
	}
	if githubPathMatchesQuery("Other/Unrelated.ttf", "Belarius Serif Narrow Regular") {
		t.Fatal("unrelated path matched foundry aliases")
	}
	longQuery := "Premier Regular Bold Italic Condensed Narrow Light Unique"
	if githubPathMatchesQuery("PremierRegularBoldItalicCondensedNarrowLight.ttf", longQuery) {
		t.Fatal("path matched after dropping a trailing query word")
	}
	if !githubPathMatchesQuery("PremierRegularBoldItalicCondensedNarrowLightUnique.ttf", longQuery) {
		t.Fatal("complete canonical path did not match a long query")
	}
}

func TestGitHubTruncatedTreeInspectsContextDirectory(t *testing.T) {
	t.Parallel()
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		switch request.URL.Path {
		case "/search/code":
			fmt.Fprint(response, `{"items":[{"name":"stylesheet.css","path":"Example #1?/stylesheet.css","repository":{"full_name":"fixture/fonts"}}]}`)
		case "/repos/fixture/fonts/git/trees/HEAD":
			fmt.Fprint(response, `{"truncated":true,"tree":[{"path":"Example #1?/Example-Regular.ttf","type":"blob","size":1200}]}`)
		case "/repos/fixture/fonts/contents/Example #1?":
			if request.URL.EscapedPath() != "/repos/fixture/fonts/contents/Example%20%231%3F" {
				t.Fatalf("escaped contents path = %q", request.URL.EscapedPath())
			}
			if request.URL.Query().Get("ref") != "HEAD" {
				t.Fatalf("contents ref = %q", request.URL.Query().Get("ref"))
			}
			fmt.Fprint(response, `[{"path":"Example #1?/Example-Regular.ttf","type":"file","size":1200},{"path":"Example #1?/Example-Bold.ttf","type":"file","size":1400}]`)
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	out := make(chan Event, 3)
	err := (GitHub{Client: server.Client(), Endpoint: server.URL + "/search/code", Token: "secret"}).Search(
		context.Background(), "Example", []string{"ttf"}, out,
	)
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 2 {
		t.Fatalf("results = %d, want deduplicated tree result plus recovered sibling", len(out))
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %#v", requests)
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
