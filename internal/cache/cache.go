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
	"sync"
	"time"

	"github.com/microck/moji/internal/provider"
)

type entry struct {
	FetchedAt    time.Time         `json:"fetched_at"`
	Results      []provider.Result `json:"results"`
	FamilyGroups []string          `json:"family_groups,omitempty"`
}

const (
	urlHealthFilename       = "url-health.json"
	defaultHealthTTL        = 30 * 24 * time.Hour
	defaultMaxHealthEntries = 256
	urlHealthLockTimeout    = 2 * time.Second
)

type urlHealthEntry struct {
	InvalidAt time.Time `json:"invalid_at"`
}

type urlHealthFile struct {
	Invalid map[string]urlHealthEntry `json:"invalid"`
}

var urlHealthMu sync.Mutex

type Store struct {
	Directory        string
	TTL              time.Duration
	HealthTTL        time.Duration
	MaxHealthEntries int
	Now              func() time.Time
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
	digest := sha256.Sum256([]byte("v3\x00" + strings.ToLower(strings.TrimSpace(query)) + "\x00" + source + "\x00" + strings.Join(ordered, ",")))
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
	for index := range cached.Results {
		if index < len(cached.FamilyGroups) {
			cached.Results[index].FamilyGroup = cached.FamilyGroups[index]
		}
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
	familyGroups := make([]string, len(results))
	for index := range results {
		familyGroups[index] = results[index].FamilyGroup
	}
	content, err := json.Marshal(entry{FetchedAt: now(), Results: results, FamilyGroups: familyGroups})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(store.Directory, store.key(query, source, formats)), content, 0o600)
}

// MarkInvalidURL records a URL only after the caller has proven that its
// response is not valid font content. Transport, server, and filesystem
// failures must not call this method because those failures can be transient.
func (store Store) MarkInvalidURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("invalid URL health entry cannot be empty")
	}

	urlHealthMu.Lock()
	defer urlHealthMu.Unlock()
	release, err := lockURLHealth(store.Directory)
	if err != nil {
		return err
	}
	defer release()

	health, err := store.readURLHealth()
	if err != nil {
		return err
	}
	now := store.now()
	health.Invalid[value] = urlHealthEntry{InvalidAt: now}
	store.pruneURLHealth(health, now)
	return store.writeURLHealth(health)
}

// IsInvalidURL is a local-only lookup. It never validates or fetches the URL,
// so ranking can suppress known-bad candidates without adding network work.
func (store Store) IsInvalidURL(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}

	invalid, err := store.InvalidURLs()
	return invalid[value], err
}

// InvalidURLs returns one in-memory snapshot for a search operation. Callers
// can check every streamed result without rereading the cache file per result.
func (store Store) InvalidURLs() (map[string]bool, error) {
	urlHealthMu.Lock()
	defer urlHealthMu.Unlock()
	health, err := store.readURLHealth()
	if err != nil {
		return nil, err
	}
	now := store.now()
	invalid := make(map[string]bool, len(health.Invalid))
	for value, entry := range health.Invalid {
		if now.Sub(entry.InvalidAt) <= store.healthTTL() {
			invalid[value] = true
		}
	}
	return invalid, nil
}

func (store Store) readURLHealth() (urlHealthFile, error) {
	health := urlHealthFile{Invalid: make(map[string]urlHealthEntry)}
	content, err := os.ReadFile(filepath.Join(store.Directory, urlHealthFilename))
	if errors.Is(err, os.ErrNotExist) {
		return health, nil
	}
	if err != nil {
		return health, err
	}
	if err := json.Unmarshal(content, &health); err != nil {
		return health, err
	}
	if health.Invalid == nil {
		health.Invalid = make(map[string]urlHealthEntry)
	}
	return health, nil
}

func (store Store) writeURLHealth(health urlHealthFile) error {
	if err := os.MkdirAll(store.Directory, 0o700); err != nil {
		return err
	}
	content, err := json.Marshal(health)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(store.Directory, ".url-health-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, filepath.Join(store.Directory, urlHealthFilename))
}

func (store Store) pruneURLHealth(health urlHealthFile, now time.Time) {
	ttl := store.healthTTL()
	for value, entry := range health.Invalid {
		if now.Sub(entry.InvalidAt) > ttl {
			delete(health.Invalid, value)
		}
	}

	maximum := store.maxHealthEntries()
	if len(health.Invalid) <= maximum {
		return
	}
	type candidate struct {
		url       string
		invalidAt time.Time
	}
	ordered := make([]candidate, 0, len(health.Invalid))
	for value, entry := range health.Invalid {
		ordered = append(ordered, candidate{url: value, invalidAt: entry.InvalidAt})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].invalidAt.Equal(ordered[j].invalidAt) {
			return ordered[i].url < ordered[j].url
		}
		return ordered[i].invalidAt.Before(ordered[j].invalidAt)
	})
	for _, entry := range ordered[:len(ordered)-maximum] {
		delete(health.Invalid, entry.url)
	}
}

func (store Store) now() time.Time {
	if store.Now != nil {
		return store.Now()
	}
	return time.Now()
}

func (store Store) healthTTL() time.Duration {
	if store.HealthTTL > 0 {
		return store.HealthTTL
	}
	return defaultHealthTTL
}

func (store Store) maxHealthEntries() int {
	if store.MaxHealthEntries > 0 {
		return store.MaxHealthEntries
	}
	return defaultMaxHealthEntries
}

func (store Store) Clear() error {
	if err := os.RemoveAll(store.Directory); err != nil {
		return err
	}
	return os.MkdirAll(store.Directory, 0o700)
}
