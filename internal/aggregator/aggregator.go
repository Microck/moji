package aggregator

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/microck/moji/internal/provider"
)

type Aggregator struct {
	Providers []provider.Provider
	Policies  map[string]provider.RatePolicy
}

func (a Aggregator) Search(ctx context.Context, query string, formats []string) <-chan provider.Event {
	out := make(chan provider.Event)
	var workers sync.WaitGroup
	workers.Add(len(a.Providers))
	for _, source := range a.Providers {
		source := source
		go func() {
			defer workers.Done()
			a.searchProvider(ctx, source, query, formats, out)
		}()
	}
	go func() {
		workers.Wait()
		close(out)
	}()
	return out
}

func (a Aggregator) searchProvider(ctx context.Context, source provider.Provider, query string, formats []string, out chan<- provider.Event) {
	name := source.Name()
	emit(ctx, out, provider.Event{Provider: name, Type: provider.EventStatus, Status: provider.StateSearching})
	policy := a.Policies[name]
	if policy.Timeout <= 0 {
		policy.Timeout = 15 * time.Second
	}
	if policy.BackoffBase <= 0 {
		policy.BackoffBase = 250 * time.Millisecond
	}

	count := 0
	for attempt := 0; attempt <= policy.Retries; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
		attemptOut := make(chan provider.Event)
		done := make(chan error, 1)
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					done <- provider.ErrUnavailable
				}
			}()
			done <- source.Search(attemptCtx, query, formats, attemptOut)
		}()

		var searchErr error
		var providerRetryAfter time.Duration
	searchLoop:
		for {
			select {
			case event := <-attemptOut:
				if event.Type == provider.EventStatus && event.Status == provider.StateThrottled {
					if event.RetryAfter > 0 {
						providerRetryAfter = event.RetryAfter
					}
					continue
				}
				if event.Type == provider.EventResult {
					count++
				}
				event.Provider = name
				if !emit(ctx, out, event) {
					cancel()
					return
				}
			case searchErr = <-done:
				break searchLoop
			case <-attemptCtx.Done():
				searchErr = attemptCtx.Err()
				break searchLoop
			}
		}
		cancel()
		if searchErr == nil {
			emit(ctx, out, provider.Event{Provider: name, Type: provider.EventStatus, Status: provider.StateDone, Count: count})
			return
		}
		if errors.Is(searchErr, provider.ErrSearchSkipped) {
			emit(ctx, out, provider.Event{Provider: name, Type: provider.EventStatus, Status: provider.StateDone, Count: count})
			return
		}
		if errors.Is(searchErr, provider.ErrBlocked) || errors.Is(searchErr, provider.ErrNonRetryable) || errors.Is(searchErr, context.Canceled) || attempt == policy.Retries {
			emit(ctx, out, provider.Event{Provider: name, Type: provider.EventStatus, Status: provider.StateFailed, Err: searchErr, Count: count})
			return
		}
		backoff := policy.BackoffBase * time.Duration(1<<attempt)
		if providerRetryAfter > 0 {
			backoff = providerRetryAfter
		}
		if policy.BackoffJitter > 0 {
			backoff += time.Duration(rand.Int63n(int64(policy.BackoffJitter)))
		}
		emit(ctx, out, provider.Event{Provider: name, Type: provider.EventStatus, Status: provider.StateThrottled, Err: searchErr, RetryAfter: backoff, Count: count})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func emit(ctx context.Context, out chan<- provider.Event, event provider.Event) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}
