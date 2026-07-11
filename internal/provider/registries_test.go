package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFontsourceSearchReturnsDirectVariantURLs(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/basier-circle" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"family":"Basier Circle","license":"OFL-1.1","variable":false,"variants":{"400":{"normal":{"latin":{"url":{"woff2":"https://cdn.example/BasierCircle-Regular.woff2"}}}}}}`))
	}))
	defer server.Close()

	events := make(chan Event, 1)
	if err := (Fontsource{Client: server.Client(), Endpoint: server.URL}).Search(context.Background(), "Basier Circle", []string{"woff2"}, events); err != nil {
		t.Fatal(err)
	}
	result := (<-events).Result
	if result.URL != "https://cdn.example/BasierCircle-Regular.woff2" || result.Filename != "Basier-Circle-regular.woff2" || result.Format != "woff2" || result.Weight != "regular" || result.License != "OFL-1.1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRegistrySearchRunsBackendsIndependently(t *testing.T) {
	t.Parallel()
	googleStarted := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/fontsource/"):
			select {
			case <-googleStarted:
				writer.WriteHeader(http.StatusNotFound)
			case <-time.After(time.Second):
				t.Error("Google Fonts was blocked behind Fontsource")
				writer.WriteHeader(http.StatusGatewayTimeout)
			}
		case request.URL.Path == "/google":
			close(googleStarted)
			_, _ = writer.Write([]byte(`@font-face { src: url(https://fonts.example/example.woff2) format('woff2'); }`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	out := make(chan Event, 2)
	source := RegistrySearch{
		Fontsource:  Fontsource{Client: server.Client(), Endpoint: server.URL + "/fontsource"},
		GoogleFonts: GoogleFonts{Client: server.Client(), Endpoint: server.URL + "/google"},
	}
	if err := source.Search(context.Background(), "Example", []string{"woff2"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 || !strings.Contains((<-out).Result.URL, "example.woff2") {
		t.Fatal("healthy registry backend result was not preserved")
	}
}

func TestGoogleFontsSearchReturnsStylesheetFontURLs(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("family") != "Ariol Serif" {
			t.Fatalf("family = %q", request.URL.Query().Get("family"))
		}
		_, _ = writer.Write([]byte(`@font-face { src: url(https://fonts.example/ariol-serif.woff2) format('woff2'); }`))
	}))
	defer server.Close()

	events := make(chan Event, 1)
	if err := (GoogleFonts{Client: server.Client(), Endpoint: server.URL}).Search(context.Background(), "Ariol Serif", []string{"woff2"}, events); err != nil {
		t.Fatal(err)
	}
	result := (<-events).Result
	if result.URL != "https://fonts.example/ariol-serif.woff2" || result.Source != "fonts.google.com" {
		t.Fatalf("result = %#v", result)
	}
}
