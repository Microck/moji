package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/microck/moji/internal/provider"
)

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
