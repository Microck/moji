package cache

import (
	"testing"
	"time"

	"github.com/microck/moji/internal/provider"
)

func TestStoreHonorsTTLAndFormatIndependentOrder(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	store := Store{Directory: t.TempDir(), TTL: time.Hour, Now: func() time.Time { return now }}
	want := []provider.Result{{Filename: "Example.otf"}}
	if err := store.Put("Example", "fixture", []string{"ttf", "otf"}, want); err != nil {
		t.Fatal(err)
	}
	got, hit, err := store.Get("example", "fixture", []string{"otf", "ttf"})
	if err != nil || !hit || got[0].Filename != want[0].Filename {
		t.Fatalf("got=%#v hit=%v err=%v", got, hit, err)
	}
	now = now.Add(2 * time.Hour)
	_, hit, err = store.Get("example", "fixture", []string{"otf", "ttf"})
	if err != nil || hit {
		t.Fatalf("expired hit=%v err=%v", hit, err)
	}
}
