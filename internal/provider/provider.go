package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrRateLimited  = errors.New("rate limited")
	ErrBlocked      = errors.New("blocked by site protection")
	ErrUnavailable  = errors.New("provider unavailable")
	ErrBadResponse  = errors.New("bad response from provider")
	ErrNonRetryable = errors.New("non-retryable provider failure")
)

func DescribeFailure(name string, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("%s timed out. Try again or increase search_timeout_seconds in the config file", name)
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf("%s search was canceled. No download was started", name)
	case errors.Is(err, ErrRateLimited):
		return fmt.Sprintf("%s reached its search limit. Wait a moment, then try again", name)
	case errors.Is(err, ErrBlocked):
		return fmt.Sprintf("%s blocked the search request. Try another enabled provider", name)
	case errors.Is(err, ErrUnavailable):
		return fmt.Sprintf("Moji couldn't connect to %s. Check your connection, then try again", name)
	case errors.Is(err, ErrNonRetryable):
		return fmt.Sprintf("%s rejected the search request. Check the query or provider configuration", name)
	case errors.Is(err, ErrBadResponse):
		return fmt.Sprintf("%s returned a response Moji couldn't use. Try again later or use another provider", name)
	case err != nil:
		return fmt.Sprintf("%s search failed: %v", name, err)
	default:
		return fmt.Sprintf("%s search failed. Try again or use another provider", name)
	}
}

type EventType int

const (
	EventResult EventType = iota
	EventStatus
)

type State int

const (
	StateSearching State = iota
	StateDone
	StateFailed
	StateThrottled
)

type Result struct {
	Name          string  `json:"name" yaml:"name"`
	Filename      string  `json:"filename" yaml:"filename"`
	Format        string  `json:"format" yaml:"format"`
	Weight        string  `json:"weight,omitempty" yaml:"weight,omitempty"`
	SizeBytes     int64   `json:"size_bytes" yaml:"size_bytes"`
	Source        string  `json:"source" yaml:"source"`
	URL           string  `json:"url" yaml:"url"`
	Trusted       bool    `json:"trusted" yaml:"trusted"`
	License       string  `json:"license" yaml:"license"`
	Variable      bool    `json:"variable,omitempty" yaml:"variable,omitempty"`
	ArchiveFormat string  `json:"archive_format,omitempty" yaml:"archive_format,omitempty"`
	ArchiveMember string  `json:"archive_member,omitempty" yaml:"archive_member,omitempty"`
	FamilyGroup   string  `json:"-" yaml:"-"`
	Score         float64 `json:"score,omitempty" yaml:"score,omitempty"`
}

func UniqueResults(results []Result) []Result {
	seen := make(map[string]bool, len(results))
	unique := make([]Result, 0, len(results))
	for _, result := range results {
		key := resultIdentity(result)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, result)
	}
	return unique
}

func resultIdentity(result Result) string {
	if result.URL != "" {
		return result.URL + "\x00" + result.ArchiveMember
	}
	return result.Source + "\x00" + result.Filename
}

type Event struct {
	Provider   string
	Type       EventType
	Result     Result
	Status     State
	Err        error
	RetryAfter time.Duration
	Count      int
}

type Provider interface {
	Name() string
	Search(ctx context.Context, query string, formats []string, out chan<- Event) error
}

type RatePolicy struct {
	Timeout       time.Duration
	Retries       int
	BackoffBase   time.Duration
	BackoffJitter time.Duration
}
