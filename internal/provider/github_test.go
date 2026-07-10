package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGitHubBuildsFormatQueriesAndRawURLs(t *testing.T) {
	t.Parallel()
	queries := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization was not set")
		}
		queries <- request.URL.Query().Get("q")
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, "{\"items\":[{\"name\":\"Example-Bold.otf\",\"path\":\"fonts/Example-Bold.otf\",\"html_url\":\"https://github.com/acme/fonts/blob/main/fonts/Example-Bold.otf\",\"repository\":{\"full_name\":\"acme/fonts\",\"default_branch\":\"main\"}}]}")
	}))
	defer server.Close()

	out := make(chan Event, 2)
	source := GitHub{Client: server.Client(), Endpoint: server.URL, Token: "secret"}
	if err := source.Search(context.Background(), "Example", []string{"otf", "ttf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	close(queries)
	if len(out) != 2 {
		t.Fatalf("result count = %d", len(out))
	}
	for event := range out {
		if event.Result.URL != "https://raw.githubusercontent.com/acme/fonts/main/fonts/Example-Bold.otf" {
			t.Fatalf("raw URL = %q", event.Result.URL)
		}
	}
	seen := strings.Join([]string{<-queries, <-queries}, " ")
	if !strings.Contains(seen, "extension:otf") || !strings.Contains(seen, "extension:ttf") {
		t.Fatalf("queries = %q", seen)
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
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
