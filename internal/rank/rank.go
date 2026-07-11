package rank

import (
	"math"
	"net/url"
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
	Variable     bool
	FamilyMember bool
}

type Weights struct {
	Format        float64 `yaml:"format"`
	FamilySize    float64 `yaml:"family_size"`
	Trusted       float64 `yaml:"trusted"`
	SizePenalty   float64 `yaml:"size_penalty"`
	WeightBonus   float64 `yaml:"weight_bonus"`
	VariableBonus float64 `yaml:"variable_bonus"`
}

type Intent struct {
	Query      string
	WantWeight string
	WantFamily bool
	Format     string
	Max        int
}

var (
	separators = regexp.MustCompile(`[^\pL\pN]+`)
	spaces     = regexp.MustCompile(`\s+`)
	variable   = regexp.MustCompile(`(?i)(?:\[[a-z,]+\]|variablefont|\bvar(?:iable)?(?:font)?\b)`)
	weights    = map[string]string{
		"thin": "thin", "hairline": "thin", "extralight": "light", "ultralight": "light",
		"light": "light", "book": "regular", "regular": "regular", "normal": "regular",
		"medium": "medium", "semibold": "semibold", "demi": "semibold", "demibold": "semibold",
		"bold": "bold", "extrabold": "bold", "ultrabold": "bold", "heavy": "black",
		"black": "black", "blk": "black", "ultra": "black",
		"reg": "regular", "roman": "regular", "med": "medium",
		"bd": "bold", "bld": "bold", "sb": "semibold", "semibd": "semibold",
	}
)

func DefaultWeights() Weights {
	return Weights{Format: 3, FamilySize: 4, Trusted: 1.5, SizePenalty: 0.5, WeightBonus: 2, VariableBonus: 1.5}
}

func ParseFilename(filename string) Tags {
	text := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	isVariable := variable.MatchString(text)
	text = variable.ReplaceAllString(text, "")
	text = splitCamelCase(text)
	parts := strings.Fields(strings.ToLower(separators.ReplaceAllString(text, " ")))

	tags := Tags{Format: format, Variable: isVariable}
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
		if compact == "sc" || compact == "smallcaps" {
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

func NormalizeQuery(query string) string {
	return strings.Join(strings.Fields(separators.ReplaceAllString(query, " ")), " ")
}

func FamilyQuery(query string) string {
	parts := strings.Fields(NormalizeQuery(query))
	for len(parts) > 1 {
		last := strings.ToLower(parts[len(parts)-1])
		if last == "italic" || last == "oblique" || last == "retina" {
			parts = parts[:len(parts)-1]
			continue
		}
		if len(parts) > 2 {
			compound := strings.ToLower(parts[len(parts)-2] + parts[len(parts)-1])
			if _, ok := weights[compound]; ok {
				parts = parts[:len(parts)-2]
				continue
			}
		}
		if _, ok := weights[last]; ok {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	return strings.Join(parts, " ")
}

func AdaptiveQueries(query string) []string {
	canonical := FamilyQuery(query)
	words := strings.Fields(canonical)
	candidates := []string{
		canonical,
		strings.Join(words, ""),
		strings.Join(words, "-"),
		strings.Join(words, "_"),
	}
	seen := make(map[string]bool, len(candidates))
	queries := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			queries = append(queries, candidate)
		}
	}
	return queries
}

func Results(results []provider.Result, query, wantedWeight string, weights Weights) []provider.Result {
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

// sourceReliability describes how consistently a source identifies a direct
// font file. It is deliberately independent from trust and license metadata:
// a reliable direct URL is not evidence that its contents are licensed or
// safe. Results uses this only after relevance and quality are tied.
func sourceReliability(result provider.Result) int {
	source := strings.ToLower(result.Source)
	switch {
	case source == "fontsource.org", source == "fonts.google.com":
		return 4
	case isRawGitHubURL(result.URL):
		return 3
	case strings.HasPrefix(source, "getfonts.cc/") || source == "getfonts.cc":
		return 2
	default:
		return 1
	}
}

func isRawGitHubURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "raw.githubusercontent.com" || host == "github.com" && strings.Contains(parsed.Path, "/raw/")
}

func Relevance(result provider.Result, query string) int {
	normalizedQuery := strings.ToLower(NormalizeQuery(query))
	return relevance(result, normalizedQuery, strings.ReplaceAll(normalizedQuery, " ", ""))
}

func relevance(result provider.Result, normalizedQuery, compactQuery string) int {
	family := strings.ToLower(NormalizeQuery(ParseFilename(result.Filename).Family))
	if normalizedQuery != "" && family == normalizedQuery {
		return 10_000
	}
	if compactQuery != "" && strings.ReplaceAll(family, " ", "") == compactQuery {
		return 9_000
	}
	compactFamily := strings.ReplaceAll(family, " ", "")
	if compactQuery != "" && compactFamily != "" &&
		(strings.HasPrefix(compactQuery, compactFamily) || strings.HasPrefix(compactFamily, compactQuery)) {
		return len(compactFamily)
	}
	if fuzzyPrefixMatch(compactFamily, compactQuery) {
		return len(compactFamily)
	}
	// Some direct CSS and archive sources use filenames such as Regular.woff2
	// or font.woff2. In that narrow case the provider's family name is the only
	// useful identity. Do not use the hint for meaningful but unrelated names.
	if family == "" || family == "font" || family == "webfont" {
		name := strings.ToLower(NormalizeQuery(result.Name))
		if name == normalizedQuery {
			return 8_000
		}
		if compactQuery != "" && strings.ReplaceAll(name, " ", "") == compactQuery {
			return 7_000
		}
	}
	return 0
}

func fuzzyPrefixMatch(family, query string) bool {
	familyRunes, queryRunes := []rune(family), []rune(query)
	if len(familyRunes) < 5 || len(queryRunes) < len(familyRunes)-1 {
		return false
	}
	for _, length := range []int{len(familyRunes) - 1, len(familyRunes), len(familyRunes) + 1} {
		if length <= len(queryRunes) && editDistance(familyRunes, queryRunes[:length]) <= 1 {
			return true
		}
	}
	return false
}

func editDistance(left, right []rune) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex := 1; leftIndex <= len(left); leftIndex++ {
		current := make([]int, len(right)+1)
		current[0] = leftIndex
		for rightIndex := 1; rightIndex <= len(right); rightIndex++ {
			cost := 0
			if left[leftIndex-1] != right[rightIndex-1] {
				cost = 1
			}
			current[rightIndex] = min(
				min(current[rightIndex-1]+1, previous[rightIndex]+1),
				previous[rightIndex-1]+cost,
			)
		}
		previous = current
	}
	return previous[len(right)]
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
		if last == "otf" || last == "ttf" || last == "woff" || last == "woff2" || last == "dfont" || last == "pfb" || last == "pfm" {
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
	intent.Query = NormalizeQuery(intent.Query)
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
	familyRank int
	styles     []string
	variable   bool
}

func Groups(results []provider.Result) []ResultGroup {
	indices := make(map[string]int)
	familyRanks := make(map[string]int)
	groups := make([]ResultGroup, 0)
	for _, result := range results {
		tags := ParseFilename(result.Filename)
		key := tags.Family + "\x00" + familyGroupOf(result)
		index, exists := indices[key]
		if !exists {
			familyRank, ranked := familyRanks[tags.Family]
			if !ranked {
				familyRank = len(familyRanks)
				familyRanks[tags.Family] = familyRank
			}
			index = len(groups)
			indices[key] = index
			groups = append(groups, ResultGroup{FamilyName: tags.Family, Source: result.Source, familyRank: familyRank})
		}
		group := &groups[index]
		group.Files = append(group.Files, result)
		group.FileCount++
		group.Weights = appendUnique(group.Weights, result.Weight)
		group.Formats = appendUnique(group.Formats, result.Format)
		style := result.Weight
		if style == "" {
			style = tags.Weight
		}
		if tags.Italic {
			style += ":italic"
		}
		group.styles = appendUnique(group.styles, style)
		group.variable = group.variable || result.Variable || tags.Variable
		if formatValue(result.Format) > formatValue(group.BestFormat) {
			group.BestFormat = result.Format
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].familyRank != groups[j].familyRank {
			return groups[i].familyRank < groups[j].familyRank
		}
		leftCoverage, rightCoverage := familyCoverage(groups[i]), familyCoverage(groups[j])
		if leftCoverage != rightCoverage {
			return leftCoverage > rightCoverage
		}
		if groups[i].FileCount != groups[j].FileCount {
			return groups[i].FileCount > groups[j].FileCount
		}
		return groups[i].Files[0].Score > groups[j].Files[0].Score
	})
	return groups
}

func familyCoverage(group ResultGroup) int {
	coverage := len(group.styles)
	if group.variable && coverage < 2 {
		return 2
	}
	return coverage
}

func familyGroupOf(result provider.Result) string {
	if result.FamilyGroup != "" {
		return result.FamilyGroup
	}
	return result.Source
}

func SelectFamily(results []provider.Result, maximum int) []provider.Result {
	if len(results) == 0 {
		return nil
	}
	best := results[0]
	bestFamily := ParseFilename(best.Filename).Family
	bestGroup := familyGroupOf(best)
	selected := make([]provider.Result, 0, maximum)
	for _, result := range results {
		if familyGroupOf(result) == bestGroup && ParseFilename(result.Filename).Family == bestFamily {
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
