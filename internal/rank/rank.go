package rank

import (
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/microck/moji/internal/provider"
)

type Tags struct {
	Family       string
	Format       string
	Weight       string
	Italic       bool
	FamilyMember bool
}

type Weights struct {
	Format      float64 `yaml:"format"`
	FamilySize  float64 `yaml:"family_size"`
	Trusted     float64 `yaml:"trusted"`
	SizePenalty float64 `yaml:"size_penalty"`
	WeightBonus float64 `yaml:"weight_bonus"`
}

type Intent struct {
	Query      string
	WantWeight string
	WantFamily bool
	Format     string
	Max        int
}

var (
	separators = regexp.MustCompile(`[-_.]+`)
	spaces     = regexp.MustCompile(`\s+`)
	variable   = regexp.MustCompile(`(?i)(?:\[wght\]|\bvar(?:iable)?\b)`)
	weights    = map[string]string{
		"thin": "thin", "hairline": "thin", "extralight": "light", "ultralight": "light",
		"light": "light", "book": "regular", "regular": "regular", "normal": "regular",
		"medium": "medium", "semibold": "semibold", "demi": "semibold", "demibold": "semibold",
		"bold": "bold", "extrabold": "bold", "ultrabold": "bold", "heavy": "black",
		"black": "black", "ultra": "black",
	}
)

func DefaultWeights() Weights {
	return Weights{Format: 3, FamilySize: 2, Trusted: 1.5, SizePenalty: 0.5, WeightBonus: 2}
}

func ParseFilename(filename string) Tags {
	text := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	text = variable.ReplaceAllString(text, "")
	text = splitCamelCase(text)
	parts := strings.Fields(strings.ToLower(separators.ReplaceAllString(text, " ")))

	tags := Tags{Format: format}
	family := make([]string, 0, len(parts))
	for index := 0; index < len(parts); index++ {
		part := parts[index]
		compact := strings.ReplaceAll(part, " ", "")
		if index+1 < len(parts) {
			// Camel-case and separators both split compound weights. Recombine
			// only pairs already defined in weights so family words stay intact.
			combined := compact + parts[index+1]
			if _, ok := weights[combined]; ok {
				compact = combined
				index++
			}
		}
		italic := false
		for _, suffix := range []string{"italic", "oblique"} {
			if strings.HasSuffix(compact, suffix) {
				tags.Italic = true
				italic = true
				compact = strings.TrimSuffix(compact, suffix)
				break
			}
		}
		if compact == "it" {
			tags.Italic = true
			continue
		}
		if weight, ok := weights[compact]; ok {
			tags.Weight = weight
			tags.FamilyMember = true
			continue
		}
		if italic && compact == "" {
			continue
		}
		if compact != "" {
			family = append(family, compact)
		}
	}
	tags.Family = spaces.ReplaceAllString(strings.Join(family, " "), " ")
	return tags
}

func splitCamelCase(value string) string {
	var out strings.Builder
	runes := []rune(value)
	for i, current := range runes {
		if i > 0 && unicode.IsUpper(current) && (unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			out.WriteByte(' ')
		}
		out.WriteRune(current)
	}
	return out.String()
}

func Results(results []provider.Result, wantedWeight string, weights Weights) []provider.Result {
	ranked := append([]provider.Result(nil), results...)
	familySizes := make(map[string]map[string]struct{})
	for i := range ranked {
		tags := ParseFilename(ranked[i].Filename)
		if ranked[i].Format == "" {
			ranked[i].Format = tags.Format
		}
		if ranked[i].Weight == "" {
			ranked[i].Weight = tags.Weight
		}
		key := tags.Family + "\x00" + ranked[i].Source
		if familySizes[key] == nil {
			familySizes[key] = make(map[string]struct{})
		}
		if ranked[i].Weight != "" {
			familySizes[key][ranked[i].Weight] = struct{}{}
		}
	}
	for i := range ranked {
		tags := ParseFilename(ranked[i].Filename)
		formatRank := map[string]float64{"otf": 3, "ttf": 2, "woff2": 1, "woff": 0.5}[ranked[i].Format]
		score := weights.Format * formatRank
		score += weights.FamilySize * math.Log2(1+float64(len(familySizes[tags.Family+"\x00"+ranked[i].Source])))
		if ranked[i].Trusted {
			score += weights.Trusted
		}
		if wantedWeight != "" && ranked[i].Weight == wantedWeight {
			score += weights.WeightBonus * 3
		}
		if ranked[i].SizeBytes > 0 && ranked[i].SizeBytes < 10_000 {
			score -= weights.SizePenalty
		}
		ranked[i].Score = score
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

func WeightOf(result provider.Result) string {
	if result.Weight != "" {
		return result.Weight
	}
	return ParseFilename(result.Filename).Weight
}

func FilterWeight(results []provider.Result, wanted string) []provider.Result {
	filtered := make([]provider.Result, 0, len(results))
	for _, result := range results {
		if WeightOf(result) == wanted {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func ParseIntent(input string) Intent {
	intent := Intent{Query: strings.TrimSpace(input), Max: 1}
	lower := strings.ToLower(intent.Query)
	for _, phrase := range []string{"entire family", "all weights"} {
		if strings.Contains(lower, phrase) {
			intent.WantFamily = true
			intent.Max = 10
			intent.Query = strings.TrimSpace(strings.Replace(lower, phrase, "", 1))
			lower = strings.ToLower(intent.Query)
			break
		}
	}
	parts := strings.Fields(intent.Query)
	if len(parts) > 1 {
		last := strings.ToLower(parts[len(parts)-1])
		if last == "otf" || last == "ttf" || last == "woff" || last == "woff2" {
			intent.Format = last
			parts = parts[:len(parts)-1]
			intent.Query = strings.Join(parts, " ")
		}
	}
	if len(parts) > 1 {
		last := strings.ToLower(parts[len(parts)-1])
		if weight, ok := weights[strings.ReplaceAll(last, "-", "")]; ok {
			intent.WantWeight = weight
			intent.Query = strings.Join(parts[:len(parts)-1], " ")
		}
	}
	return intent
}

type ResultGroup struct {
	FamilyName string
	Source     string
	Files      []provider.Result
	Weights    []string
	Formats    []string
	BestFormat string
	FileCount  int
}

func Groups(results []provider.Result) []ResultGroup {
	indices := make(map[string]int)
	groups := make([]ResultGroup, 0)
	for _, result := range results {
		tags := ParseFilename(result.Filename)
		key := tags.Family + "\x00" + result.Source
		index, exists := indices[key]
		if !exists {
			index = len(groups)
			indices[key] = index
			groups = append(groups, ResultGroup{FamilyName: tags.Family, Source: result.Source})
		}
		group := &groups[index]
		group.Files = append(group.Files, result)
		group.FileCount++
		group.Weights = appendUnique(group.Weights, result.Weight)
		group.Formats = appendUnique(group.Formats, result.Format)
		if formatValue(result.Format) > formatValue(group.BestFormat) {
			group.BestFormat = result.Format
		}
	}
	return groups
}

func SelectFamily(results []provider.Result, maximum int) []provider.Result {
	if len(results) == 0 {
		return nil
	}
	best := results[0]
	bestFamily := ParseFilename(best.Filename).Family
	selected := make([]provider.Result, 0, maximum)
	for _, result := range results {
		if result.Source == best.Source && ParseFilename(result.Filename).Family == bestFamily {
			selected = append(selected, result)
			if len(selected) == maximum {
				break
			}
		}
	}
	return selected
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func formatValue(format string) int {
	return map[string]int{"otf": 4, "ttf": 3, "woff2": 2, "woff": 1}[format]
}
