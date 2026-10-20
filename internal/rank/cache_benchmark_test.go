package rank

import (
	"fmt"
	"github.com/microck/moji/internal/provider"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Frozen pre-optimization implementation, retained to compare full ordering,
// scores, and metadata rather than only the first result.
func baselineResults(results []provider.Result, query, wantedWeight string, weights Weights) []provider.Result {
	normalizedQuery := strings.ToLower(NormalizeQuery(query))
	compactQuery := strings.ReplaceAll(normalizedQuery, " ", "")
	ranked := make([]provider.Result, 0, len(results))
	for _, result := range results {
		if compactQuery == "" || relevance(result, normalizedQuery, compactQuery) > 0 {
			ranked = append(ranked, result)
		}
	}
	familySizes := make(map[string]map[string]struct{})
	for i := range ranked {
		tags := ParseFilename(ranked[i].Filename)
		if ranked[i].Format == "" {
			ranked[i].Format = tags.Format
		}
		if ranked[i].Weight == "" {
			ranked[i].Weight = tags.Weight
		}
		if tags.Variable {
			ranked[i].Variable = true
		}
		key := tags.Family + "\x00" + familyGroupOf(ranked[i])
		if familySizes[key] == nil {
			familySizes[key] = make(map[string]struct{})
		}
		if ranked[i].Weight != "" {
			familySizes[key][ranked[i].Weight] = struct{}{}
		}
	}
	for i := range ranked {
		tags := ParseFilename(ranked[i].Filename)
		formatRank := map[string]float64{"otf": 3, "ttf": 2, "dfont": 1.5, "pfb": 1, "woff2": 0.75, "woff": 0.5, "pfm": 0.1}[ranked[i].Format]
		score := weights.Format * formatRank
		score += weights.FamilySize * math.Log2(1+float64(len(familySizes[tags.Family+"\x00"+familyGroupOf(ranked[i])])))
		if ranked[i].Trusted {
			score += weights.Trusted
		}
		if wantedWeight != "" && ranked[i].Weight == wantedWeight {
			score += weights.WeightBonus * 3
		}
		if ranked[i].Variable {
			score += weights.VariableBonus
		}
		if ranked[i].SizeBytes > 0 && ranked[i].SizeBytes < 10_000 {
			score -= weights.SizePenalty
		}
		ranked[i].Score = score
	}
	sort.Slice(ranked, func(i, j int) bool {
		leftRelevance := relevance(ranked[i], normalizedQuery, compactQuery)
		rightRelevance := relevance(ranked[j], normalizedQuery, compactQuery)
		if leftRelevance != rightRelevance {
			return leftRelevance > rightRelevance
		}
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		leftReliability := sourceReliability(ranked[i])
		rightReliability := sourceReliability(ranked[j])
		if leftReliability != rightReliability {
			return leftReliability > rightReliability
		}
		if ranked[i].Filename != ranked[j].Filename {
			return ranked[i].Filename < ranked[j].Filename
		}
		if ranked[i].Source != ranked[j].Source {
			return ranked[i].Source < ranked[j].Source
		}
		return ranked[i].URL < ranked[j].URL
	})
	return ranked
}

func rankingFixture() []provider.Result {
	results := make([]provider.Result, 600)
	families := []string{"Inter", "Inter var", "Example", "Font", "SourceSans", "AvenirNext", "ProximaNova", "HelveticaNeue"}
	weights := []string{"Regular", "Bold", "Light", "SemiBoldItalic"}
	for i := range results {
		results[i] = provider.Result{Name: families[i%len(families)], Filename: fmt.Sprintf("%s-%s.ttf", families[i%len(families)], weights[(i/7)%len(weights)]), Source: fmt.Sprintf("source-%d", i%9), URL: fmt.Sprintf("https://example.test/font-%d.ttf", i), SizeBytes: int64(8000 + i*10), Trusted: i%3 == 0}
	}
	return results
}
func TestCachedRankingPreservesCompleteResults(t *testing.T) {
	input := rankingFixture()
	before := append([]provider.Result(nil), input...)
	for _, query := range []string{"", "Inter", "Source Sans", "Avenir", "unmatched", "FONT", "Proxima Nova", "Helvetic"} {
		for _, weight := range []string{"", "bold", "light"} {
			got := Results(input, query, weight, DefaultWeights())
			want := baselineResults(input, query, weight, DefaultWeights())
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ranking changed for %q/%q", query, weight)
			}
		}
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("ranking mutated caller results")
	}
}
func BenchmarkRankingBaseline(b *testing.B) {
	input := rankingFixture()
	b.ReportAllocs()
	for b.Loop() {
		baselineResults(input, "Inter", "", DefaultWeights())
	}
}
func BenchmarkRankingCached(b *testing.B) {
	input := rankingFixture()
	b.ReportAllocs()
	for b.Loop() {
		Results(input, "Inter", "", DefaultWeights())
	}
}
