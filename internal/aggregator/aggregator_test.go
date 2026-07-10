package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/microck/moji/internal/provider"
)

type fakeProvider struct {
	name     string
	failures int
	calls    int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Search(ctx context.Context, query string, formats []string, out chan<- provider.Event) error {
	f.calls++
	if f.calls <= f.failures {
		return provider.ErrRateLimited
	}
	out <- provider.Event{Type: provider.EventResult, Result: provider.Result{Filename: query + "." + formats[0]}}
	return nil
}

func TestSearchRetriesOneProviderWithoutLosingAnother(t *testing.T) {
	t.Parallel()
	flaky := &fakeProvider{name: "flaky", failures: 1}
	steady := &fakeProvider{name: "steady"}
	aggregate := Aggregator{
		Providers: []provider.Provider{flaky, steady},
		Policies:  map[string]provider.RatePolicy{"flaky": {Retries: 1, Timeout: time.Second, BackoffBase: time.Millisecond}},
	}
	results := 0
	throttled := false
	for event := range aggregate.Search(context.Background(), "Example", []string{"otf"}) {
		if event.Type == provider.EventResult {
			results++
		}
		if event.Status == provider.StateThrottled {
			throttled = true
		}
	}
	if results != 2 || !throttled || flaky.calls != 2 {
		t.Fatalf("results=%d throttled=%v flaky calls=%d", results, throttled, flaky.calls)
	}
}

type blockedProvider struct{}

func (blockedProvider) Name() string { return "blocked" }
func (blockedProvider) Search(context.Context, string, []string, chan<- provider.Event) error {
	return provider.ErrBlocked
}

type retryAfterProvider struct{ calls int }

func (source *retryAfterProvider) Name() string { return "retry-after" }
func (source *retryAfterProvider) Search(ctx context.Context, query string, formats []string, out chan<- provider.Event) error {
	source.calls++
	if source.calls == 1 {
		out <- provider.Event{Type: provider.EventStatus, Status: provider.StateThrottled, RetryAfter: time.Millisecond}
		return provider.ErrRateLimited
	}
	return nil
}

func TestSearchUsesProviderRetryAfter(t *testing.T) {
	t.Parallel()
	source := &retryAfterProvider{}
	aggregate := Aggregator{Providers: []provider.Provider{source}, Policies: map[string]provider.RatePolicy{"retry-after": {Retries: 1, Timeout: time.Second, BackoffBase: time.Hour}}}
	started := time.Now()
	for range aggregate.Search(context.Background(), "Example", []string{"otf"}) {
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("provider Retry-After was ignored: %s", elapsed)
	}
}

type panicProvider struct{}

func (panicProvider) Name() string { return "panic" }
func (panicProvider) Search(context.Context, string, []string, chan<- provider.Event) error {
	panic("fixture panic")
}

func TestProviderPanicDoesNotStopOtherProviders(t *testing.T) {
	t.Parallel()
	steady := &fakeProvider{name: "steady"}
	aggregate := Aggregator{Providers: []provider.Provider{panicProvider{}, steady}, Policies: map[string]provider.RatePolicy{
		"panic": {Timeout: time.Second}, "steady": {Timeout: time.Second},
	}}
	results, failures := 0, 0
	for event := range aggregate.Search(context.Background(), "Example", []string{"otf"}) {
		if event.Type == provider.EventResult {
			results++
		}
		if event.Status == provider.StateFailed {
			failures++
		}
	}
	if results != 1 || failures != 1 {
		t.Fatalf("results=%d failures=%d", results, failures)
	}
}

func TestBlockedProviderIsNotRetried(t *testing.T) {
	t.Parallel()
	aggregate := Aggregator{Providers: []provider.Provider{blockedProvider{}}, Policies: map[string]provider.RatePolicy{"blocked": {Retries: 3, Timeout: time.Second}}}
	var final provider.Event
	for event := range aggregate.Search(context.Background(), "Example", []string{"ttf"}) {
		final = event
	}
	if final.Status != provider.StateFailed || !errors.Is(final.Err, provider.ErrBlocked) {
		t.Fatalf("final event = %#v", final)
	}
}
