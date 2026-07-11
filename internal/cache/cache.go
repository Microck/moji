package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/microck/moji/internal/provider"
)

type entry struct {
	FetchedAt time.Time         `json:"fetched_at"`
	Results   []provider.Result `json:"results"`
}
type Store struct {
	Directory string
	TTL       time.Duration
	Now       func() time.Time
}

type CachedProvider struct {
	Source provider.Provider
	Store  Store
	Bypass bool
}

func (cached CachedProvider) Name() string { return cached.Source.Name() }
func (cached CachedProvider) Search(ctx context.Context, query string, formats []string, out chan<- provider.Event) error {
	if !cached.Bypass {
		results, hit, err := cached.Store.Get(query, cached.Name(), formats)
		if err == nil && hit {
			for _, result := range results {
				out <- provider.Event{Type: provider.EventResult, Result: result}
			}
			return nil
		}
	}
	captured := make([]provider.Result, 0)
	events := make(chan provider.Event)
	done := make(chan error, 1)
	go func() { done <- cached.Source.Search(ctx, query, formats, events) }()
	for {
		select {
		case event := <-events:
			if event.Type == provider.EventResult {
				captured = append(captured, event.Result)
			}
			out <- event
		case err := <-done:
			if err == nil && !cached.Bypass {
				_ = cached.Store.Put(query, cached.Name(), formats, captured)
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func DefaultDirectory() (string, error) {
	if root := os.Getenv("XDG_CACHE_HOME"); root != "" {
		return filepath.Join(root, "moji"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "moji"), nil
}

func (store Store) key(query, source string, formats []string) string {
	ordered := append([]string(nil), formats...)
	sort.Strings(ordered)
	digest := sha256.Sum256([]byte("v2\x00" + strings.ToLower(strings.TrimSpace(query)) + "\x00" + source + "\x00" + strings.Join(ordered, ",")))
	return hex.EncodeToString(digest[:]) + ".json"
}

func (store Store) Get(query, source string, formats []string) ([]provider.Result, bool, error) {
	content, err := os.ReadFile(filepath.Join(store.Directory, store.key(query, source, formats)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var cached entry
	if err := json.Unmarshal(content, &cached); err != nil {
		return nil, false, err
	}
	now := time.Now
	if store.Now != nil {
		now = store.Now
	}
	if store.TTL <= 0 || now().Sub(cached.FetchedAt) > store.TTL {
		return nil, false, nil
	}
	return cached.Results, true, nil
}

func (store Store) Put(query, source string, formats []string, results []provider.Result) error {
	if err := os.MkdirAll(store.Directory, 0o700); err != nil {
		return err
	}
	now := time.Now
	if store.Now != nil {
		now = store.Now
	}
	content, err := json.Marshal(entry{FetchedAt: now(), Results: results})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(store.Directory, store.key(query, source, formats)), content, 0o600)
}

func (store Store) Clear() error {
	if err := os.RemoveAll(store.Directory); err != nil {
		return err
	}
	return os.MkdirAll(store.Directory, 0o700)
}
