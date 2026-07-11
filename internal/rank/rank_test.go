package rank

import (
	"reflect"
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
		variable bool
	}{
		{"ProximaNova-Bold.ttf", "proxima nova", "bold", "ttf", false, false},
		{"Proxima-Nova-Regular.otf", "proxima nova", "regular", "otf", false, false},
		{"HelveticaNeueLTStd-Light.otf", "helvetica neue lt std", "light", "otf", false, false},
		{"AvenirNextCondensed-Bold.ttf", "avenir next condensed", "bold", "ttf", false, false},
		{"FF-Meta-Pro-Normal-Italic.otf", "ff meta pro", "regular", "otf", true, false},
		{"ExampleSC-Bd.pfb", "example", "bold", "pfb", false, false},
		{"SourceSans-SemiBoldItalic.woff2", "source sans", "semibold", "woff2", true, false},
		{"Avenir-DemiBoldOblique.ttf", "avenir", "semibold", "ttf", true, false},
		{"Inter-ExtraBold.otf", "inter", "bold", "otf", false, false},
		{"Inter-Extra-Bold.ttf", "inter", "bold", "ttf", false, false},
		{"Inter-UltraLightItalic.woff2", "inter", "light", "woff2", true, false},
		{"Inter.var.ttf", "inter", "", "ttf", false, true},
		{"Inter[wdth,wght].ttf", "inter", "", "ttf", false, true},
		{"font-awesome-webfont.woff2", "font awesome webfont", "", "woff2", false, false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.filename, func(t *testing.T) {
			t.Parallel()
			tags := ParseFilename(test.filename)
			if tags.Family != test.family || tags.Weight != test.weight || tags.Format != test.format || tags.Italic != test.italic || tags.Variable != test.variable {
				t.Fatalf("ParseFilename(%q) = %#v", test.filename, tags)
			}
		})
	}
}

func TestRankPrefersCompleteFamilySource(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "single"},
		{Filename: "Example-Regular.ttf", Format: "ttf", Source: "family"},
		{Filename: "Example-Bold.ttf", Format: "ttf", Source: "family"},
		{Filename: "Example-Light.ttf", Format: "ttf", Source: "family"},
	}
	if got := Results(results, "Example", "", DefaultWeights())[0].Source; got != "family" {
		t.Fatalf("best source = %q", got)
	}
}

func TestRankRecognizesAndPrefersVariableFont(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Inter-Regular.ttf", Format: "ttf", Source: "same"},
		{Filename: "Inter[wdth,wght].ttf", Format: "ttf", Source: "same"},
	}
	ranked := Results(results, "Inter", "", DefaultWeights())
	if !ranked[0].Variable {
		t.Fatalf("ranked = %#v", ranked)
	}
}

func TestRankPrefersRequestedWeightThenFormat(t *testing.T) {
	t.Parallel()

	results := []provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Trusted: true, SizeBytes: 100_000},
		{Filename: "Example-Bold.ttf", Format: "ttf", Weight: "bold", Trusted: true, SizeBytes: 100_000},
		{Filename: "Example-Bold.woff2", Format: "woff2", Weight: "bold", Trusted: false, SizeBytes: 50_000},
	}
	ranked := Results(results, "Example", "bold", DefaultWeights())
	if ranked[0].Filename != "Example-Bold.ttf" {
		t.Fatalf("best result = %q, want requested trusted bold", ranked[0].Filename)
	}
}

func TestRankPrefersRelevantFamilyOverQuality(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Montserrat-Regular.otf", Format: "otf", Trusted: true, Source: "trusted"},
		{Filename: "Inter-Regular.woff", Format: "woff", Source: "other"},
	}
	if got := Results(results, "inter", "", DefaultWeights())[0].Filename; got != "Inter-Regular.woff" {
		t.Fatalf("best result = %q", got)
	}
}

func TestRankUsesSourceReliabilityOnlyAsQualityTieBreaker(t *testing.T) {
	t.Parallel()

	results := []provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "downloads.example.com", URL: "https://downloads.example.com/Example-Regular.otf"},
		{Filename: "Example-Regular.otf", Format: "otf", Source: "getfonts.cc/example", URL: "https://getfonts.cc/example/Example-Regular.otf"},
		{Filename: "Example-Regular.otf", Format: "otf", Source: "github.com/example/fonts", URL: "https://raw.githubusercontent.com/example/fonts/main/Example-Regular.otf"},
		{Filename: "Example-Regular.otf", Format: "otf", Source: "fontsource.org", URL: "https://cdn.jsdelivr.net/fontsource/example/Example-Regular.otf"},
	}

	ranked := Results(results, "Example", "", DefaultWeights())
	want := []string{"fontsource.org", "github.com/example/fonts", "getfonts.cc/example", "downloads.example.com"}
	for index, source := range want {
		if ranked[index].Source != source {
			t.Fatalf("ranked[%d].Source = %q, want %q; ranked=%#v", index, ranked[index].Source, source, ranked)
		}
	}
}

func TestRankSourceReliabilityDoesNotOverrideRelevance(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Example-Sans-Regular.woff", Format: "woff", Source: "downloads.example.com"},
		{Filename: "Example-Regular.otf", Format: "otf", Source: "fontsource.org"},
	}

	if got := Results(results, "Example Sans", "", DefaultWeights())[0].Source; got != "downloads.example.com" {
		t.Fatalf("best source = %q, want more relevant arbitrary host", got)
	}
}

func TestRankSourceReliabilityDoesNotOverrideFamilyCompleteness(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "downloads.example.com"},
		{Filename: "Example-Bold.otf", Format: "otf", Weight: "bold", Source: "downloads.example.com"},
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "fontsource.org"},
	}

	if got := Results(results, "Example", "", DefaultWeights())[0].Source; got != "downloads.example.com" {
		t.Fatalf("best source = %q, want source with complete family", got)
	}
}

func TestSourceReliabilityIsIndependentFromTrustAndLicense(t *testing.T) {
	t.Parallel()
	structured := provider.Result{Source: "fonts.google.com", Trusted: false, License: ""}
	arbitrary := provider.Result{Source: "downloads.example.com", Trusted: true, License: "OFL-1.1"}
	if sourceReliability(structured) <= sourceReliability(arbitrary) {
		t.Fatalf("structured reliability must not derive from Trusted or License")
	}
}

func TestRankKeepsOneCharacterSearchCorrection(t *testing.T) {
	t.Parallel()
	results := []provider.Result{{Filename: "Bariol Serif.ttf", Source: "search"}}
	ranked := Results(results, "ariol serif italic", "", DefaultWeights())
	if len(ranked) != 1 || ranked[0].Filename != "Bariol Serif.ttf" {
		t.Fatalf("ranked = %#v", ranked)
	}
}

func TestRankUsesFamilyHintOnlyForOpaqueFilenames(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Name: "Inter", Filename: "Regular.woff2", Source: "css"},
		{Name: "Inter", Filename: "Montserrat-Regular.woff2", Source: "unrelated"},
	}
	ranked := Results(results, "Inter", "", DefaultWeights())
	if len(ranked) != 1 || ranked[0].Filename != "Regular.woff2" {
		t.Fatalf("ranked = %#v", ranked)
	}
}

func TestFamilyQueryRemovesStyleAndWeightSuffixes(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"BASIER narrow regular":     "BASIER narrow",
		"bariol_serif italic":       "bariol serif",
		"geo manist extra-light":    "geo manist",
		"ibm plex sans bold italic": "ibm plex sans",
		"fira-code retina":          "fira code",
		"Source Sans 3":             "Source Sans 3",
	}
	for input, want := range tests {
		if got := FamilyQuery(input); got != want {
			t.Errorf("FamilyQuery(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAdaptiveQueriesUseCommonFilenameConventions(t *testing.T) {
	want := []string{"Proxima Nova", "ProximaNova", "Proxima-Nova", "Proxima_Nova"}
	got := AdaptiveQueries("Proxima Nova Bold")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("queries = %#v, want %#v", got, want)
	}
}

func TestRankReturnsNonNilEmptySlice(t *testing.T) {
	t.Parallel()
	if ranked := Results(nil, "missing", "", DefaultWeights()); ranked == nil {
		t.Fatal("empty ranked results must encode as []")
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
		{"legacy family pfb", Intent{Query: "legacy family", Format: "pfb", Max: 1}},
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

func TestGroupsDoNotMixSameHostResultsFromDifferentRepositories(t *testing.T) {
	t.Parallel()
	results := []provider.Result{
		{Filename: "Example-Regular.otf", Source: "raw.githubusercontent.com", FamilyGroup: "github.com/one/fonts"},
		{Filename: "Example-Bold.otf", Source: "raw.githubusercontent.com", FamilyGroup: "github.com/two/fonts"},
	}
	groups := Groups(results)
	if len(groups) != 2 {
		t.Fatalf("groups = %#v, want separate repository groups", groups)
	}
	if selected := SelectFamily(results, 10); len(selected) != 1 {
		t.Fatalf("selected = %#v, want one repository only", selected)
	}
}
