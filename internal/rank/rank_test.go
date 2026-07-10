package rank

import (
	"testing"

	"github.com/microck/moji/internal/provider"
)

func TestParseFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename string
		family   string
		weight   string
		format   string
		italic   bool
	}{
		{"ProximaNova-Bold.ttf", "proxima nova", "bold", "ttf", false},
		{"Proxima-Nova-Regular.otf", "proxima nova", "regular", "otf", false},
		{"HelveticaNeueLTStd-Light.otf", "helvetica neue lt std", "light", "otf", false},
		{"SourceSans-SemiBoldItalic.woff2", "source sans", "semibold", "woff2", true},
		{"Avenir-DemiBoldOblique.ttf", "avenir", "semibold", "ttf", true},
		{"Inter.var.ttf", "inter", "", "ttf", false},
		{"Inter[wght].ttf", "inter", "", "ttf", false},
		{"font-awesome-webfont.woff2", "font awesome webfont", "", "woff2", false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.filename, func(t *testing.T) {
			t.Parallel()
			tags := ParseFilename(test.filename)
			if tags.Family != test.family || tags.Weight != test.weight || tags.Format != test.format || tags.Italic != test.italic {
				t.Fatalf("ParseFilename(%q) = %#v", test.filename, tags)
			}
		})
	}
}

func TestRankPrefersRequestedWeightThenFormat(t *testing.T) {
	t.Parallel()

	results := []provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Trusted: true, SizeBytes: 100_000},
		{Filename: "Example-Bold.ttf", Format: "ttf", Weight: "bold", Trusted: true, SizeBytes: 100_000},
		{Filename: "Example-Bold.woff2", Format: "woff2", Weight: "bold", Trusted: false, SizeBytes: 50_000},
	}
	ranked := Results(results, "bold", DefaultWeights())
	if ranked[0].Filename != "Example-Bold.ttf" {
		t.Fatalf("best result = %q, want requested trusted bold", ranked[0].Filename)
	}
}

func TestParseIntent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  Intent
	}{
		{"proxima nova bold", Intent{Query: "proxima nova", WantWeight: "bold", Max: 1}},
		{"helvetica neue entire family", Intent{Query: "helvetica neue", WantFamily: true, Max: 10}},
		{"FF Meta Serif regular", Intent{Query: "FF Meta Serif", WantWeight: "regular", Max: 1}},
		{"proxima nova bold otf", Intent{Query: "proxima nova", WantWeight: "bold", Format: "otf", Max: 1}},
	}
	for _, test := range tests {
		got := ParseIntent(test.input)
		if got != test.want {
			t.Errorf("ParseIntent(%q) = %#v, want %#v", test.input, got, test.want)
		}
	}
}

func TestGroupsAndFamilySelectionStayWithinBestSource(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Example-Bold.otf", Format: "otf", Weight: "bold", Source: "best", Score: 10},
		{Filename: "Example-Regular.ttf", Format: "ttf", Weight: "regular", Source: "best", Score: 9},
		{Filename: "Example-Light.otf", Format: "otf", Weight: "light", Source: "other", Score: 8},
	}
	groups := Groups(results)
	if len(groups) != 2 || groups[0].FileCount != 2 || groups[0].BestFormat != "otf" {
		t.Fatalf("groups = %#v", groups)
	}
	selected := SelectFamily(results, 10)
	if len(selected) != 2 || selected[1].Source != "best" {
		t.Fatalf("selected = %#v", selected)
	}
}
