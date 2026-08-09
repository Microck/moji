package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/microck/moji/internal/provider"
)

type skippedProvider struct{}

func (skippedProvider) Name() string { return "skipped" }
func (skippedProvider) Search(context.Context, string, []string, chan<- provider.Event) error {
	return provider.ErrSearchSkipped
}

type forwardingProvider struct {
	forwarded chan struct{}
	finished  chan struct{}
}

func (forwardingProvider) Name() string { return "forwarding" }
func (source forwardingProvider) Search(_ context.Context, _ string, _ []string, out chan<- provider.Event) error {
	out <- provider.Event{Type: provider.EventResult, Result: provider.Result{Filename: "Example-Regular.otf"}}
	close(source.forwarded)
	out <- provider.Event{Type: provider.EventResult, Result: provider.Result{Filename: "Example-Bold.otf"}}
	close(source.finished)
	return nil
}

type namespacedProvider struct {
	namespace string
	filename  string
	calls     *int
}

func (source namespacedProvider) Name() string           { return "namespaced" }
func (source namespacedProvider) CacheNamespace() string { return source.namespace }
func (source namespacedProvider) CacheQuery(query string) string {
	return strings.ReplaceAll(query, " ", "")
}
func (source namespacedProvider) Search(_ context.Context, _ string, _ []string, out chan<- provider.Event) error {
	(*source.calls)++
	out <- provider.Event{Type: provider.EventResult, Result: provider.Result{Filename: source.filename}}
	return nil
}

func TestCachedProviderDoesNotCacheSkippedSearch(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir(), TTL: time.Hour}
	cached := CachedProvider{Source: skippedProvider{}, Store: store}
	err := cached.Search(context.Background(), "ProximaNova", []string{"ttf"}, make(chan provider.Event, 1))
	if !errors.Is(err, provider.ErrSearchSkipped) {
		t.Fatalf("search error = %v, want skipped signal", err)
	}
	_, hit, getErr := store.Get("ProximaNova", "skipped", []string{"ttf"})
	if getErr != nil || hit {
		t.Fatalf("cache hit = %v, error = %v; skipped search must not be cached", hit, getErr)
	}
}

func TestCachedProviderCancelsBlockedCacheHitForwarding(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir(), TTL: time.Hour}
	if err := store.Put("Example", "skipped", []string{"otf"}, []provider.Result{{Filename: "Example.otf"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (CachedProvider{Source: skippedProvider{}, Store: store}).Search(
			ctx, "Example", []string{"otf"}, make(chan provider.Event),
		)
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cache-hit forwarding did not stop after cancellation")
	}
}

func TestCachedProviderReturnsPreCanceledContextForEmptyCacheHit(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir(), TTL: time.Hour}
	if err := store.Put("Example", "skipped", []string{"otf"}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (CachedProvider{Source: skippedProvider{}, Store: store}).Search(
		ctx, "Example", []string{"otf"}, make(chan provider.Event, 1),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestCachedProviderCancelsBlockedLiveForwarding(t *testing.T) {
	t.Parallel()
	forwarded := make(chan struct{})
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (CachedProvider{Source: forwardingProvider{forwarded: forwarded, finished: finished}, Bypass: true}).Search(
			ctx, "Example", []string{"otf"}, make(chan provider.Event),
		)
	}()
	<-forwarded
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("live forwarding did not stop after cancellation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("live forwarding stranded the source provider")
	}
}

func TestCachedProviderSeparatesProviderNamespaces(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir(), TTL: time.Hour}
	firstCalls, secondCalls := 0, 0
	for _, test := range []struct {
		source namespacedProvider
		want   string
	}{
		{source: namespacedProvider{namespace: "namespaced:first", filename: "First.otf", calls: &firstCalls}, want: "First.otf"},
		{source: namespacedProvider{namespace: "namespaced:second", filename: "Second.otf", calls: &secondCalls}, want: "Second.otf"},
	} {
		out := make(chan provider.Event, 1)
		if err := (CachedProvider{Source: test.source, Store: store}).Search(context.Background(), "Example", []string{"otf"}, out); err != nil {
			t.Fatal(err)
		}
		if got := (<-out).Result.Filename; got != test.want {
			t.Fatalf("filename = %q, want %q", got, test.want)
		}
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("provider calls = %d, %d; want 1, 1", firstCalls, secondCalls)
	}
}

func TestCachedProviderUsesCanonicalProviderQuery(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir(), TTL: time.Hour}
	calls := 0
	source := namespacedProvider{namespace: "namespaced:one", filename: "Example.otf", calls: &calls}
	for _, query := range []string{"Example Font", "ExampleFont"} {
		out := make(chan provider.Event, 1)
		if err := (CachedProvider{Source: source, Store: store}).Search(context.Background(), query, []string{"otf"}, out); err != nil {
			t.Fatal(err)
		}
		if got := (<-out).Result.Filename; got != "Example.otf" {
			t.Fatalf("filename = %q, want Example.otf", got)
		}
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestStoreHonorsTTLAndFormatIndependentOrder(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := Store{Directory: t.TempDir(), TTL: time.Hour, Now: func() time.Time { return now }}
	want := []provider.Result{{Filename: "Example.otf", FamilyGroup: "github.com/example/fonts"}}
	if err := store.Put("Example", "fixture", []string{"ttf", "otf"}, want); err != nil {
		t.Fatal(err)
	}
	got, hit, err := store.Get("example", "fixture", []string{"otf", "ttf"})
	if err != nil || !hit || got[0].Filename != want[0].Filename || got[0].FamilyGroup != want[0].FamilyGroup {
		t.Fatalf("got=%#v hit=%v err=%v", got, hit, err)
	}
	now = now.Add(2 * time.Hour)
	_, hit, err = store.Get("example", "fixture", []string{"otf", "ttf"})
	if err != nil || hit {
		t.Fatalf("expired hit=%v err=%v", hit, err)
	}
}

func TestStoreUsesCurrentCacheSchemaKey(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir()}
	current := store.key("Example", "fixture", []string{"otf"})
	legacyDigest := sha256.Sum256([]byte("v2\x00example\x00fixture\x00otf"))
	legacy := hex.EncodeToString(legacyDigest[:]) + ".json"
	if current == legacy {
		t.Fatal("current result cache key still accepts pre-FamilyGroup entries")
	}
}

func TestStorePersistsInvalidURLHealthUntilTTL(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	directory := t.TempDir()
	store := Store{
		Directory:        directory,
		HealthTTL:        time.Hour,
		MaxHealthEntries: 10,
		Now:              func() time.Time { return now },
	}

	if err := store.MarkInvalidURL("https://example.com/not-a-font.ttf"); err != nil {
		t.Fatal(err)
	}

	reopened := store
	if invalid, err := reopened.IsInvalidURL("https://example.com/not-a-font.ttf"); err != nil || !invalid {
		t.Fatalf("invalid=%v err=%v, want persisted invalid URL", invalid, err)
	}

	now = now.Add(2 * time.Hour)
	if invalid, err := reopened.IsInvalidURL("https://example.com/not-a-font.ttf"); err != nil || invalid {
		t.Fatalf("invalid=%v err=%v, want expired URL health", invalid, err)
	}
}

func TestStoreBoundsInvalidURLHealthByMostRecentFailure(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := Store{
		Directory:        t.TempDir(),
		HealthTTL:        time.Hour,
		MaxHealthEntries: 2,
		Now:              func() time.Time { return now },
	}

	for _, url := range []string{"https://example.com/old.ttf", "https://example.com/middle.ttf", "https://example.com/new.ttf"} {
		if err := store.MarkInvalidURL(url); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}

	for url, want := range map[string]bool{
		"https://example.com/old.ttf":    false,
		"https://example.com/middle.ttf": true,
		"https://example.com/new.ttf":    true,
	} {
		if invalid, err := store.IsInvalidURL(url); err != nil || invalid != want {
			t.Errorf("IsInvalidURL(%q) = %v, %v; want %v, nil", url, invalid, err, want)
		}
	}
}

func TestStoreDoesNotRecordEmptyInvalidURL(t *testing.T) {
	t.Parallel()
	store := Store{Directory: t.TempDir(), HealthTTL: time.Hour, MaxHealthEntries: 10}
	if err := store.MarkInvalidURL("  "); err == nil {
		t.Fatal("MarkInvalidURL(empty) error = nil")
	}
}

func TestStoreClearRemovesSearchAndURLHealthCaches(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	store := Store{Directory: directory, TTL: time.Hour, HealthTTL: time.Hour, MaxHealthEntries: 10}
	if err := store.Put("Example", "fixture", []string{"otf"}, []provider.Result{{Filename: "Example.otf"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInvalidURL("https://example.com/invalid.otf"); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cache directory contains %v after Clear", entries)
	}
	if _, err := os.Stat(filepath.Join(directory, urlHealthFilename)); !os.IsNotExist(err) {
		t.Fatalf("health cache stat error = %v, want not exist", err)
	}
}
