package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDescribeFailureProvidesRecovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err      error
		contains string
	}{
		{ErrRateLimited, "Wait a moment"},
		{ErrUnavailable, "Check your connection"},
		{ErrBadResponse, "use another provider"},
		{context.DeadlineExceeded, "increase search_timeout_seconds"},
		{context.Canceled, "No download was started"},
	}
	for _, test := range tests {
		message := DescribeFailure("fixture", test.err)
		if !strings.Contains(message, test.contains) {
			t.Errorf("DescribeFailure(%v) = %q", test.err, message)
		}
	}
	if message := DescribeFailure("fixture", errors.New("specific cause")); !strings.Contains(message, "specific cause") {
		t.Fatalf("specific failure lost its cause: %q", message)
	}
}

func TestUniqueResultsUsesURLOrSourceAndFilename(t *testing.T) {
	t.Parallel()
	results := UniqueResults([]Result{
		{URL: "https://example.test/a", Filename: "A.otf"},
		{URL: "https://example.test/a", Filename: "duplicate.otf"},
		{Source: "fixture", Filename: "B.otf"},
		{Source: "fixture", Filename: "B.otf"},
	})
	if len(results) != 2 {
		t.Fatalf("unique results = %#v", results)
	}
}
