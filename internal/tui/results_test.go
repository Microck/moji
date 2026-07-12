package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
)

func sizedModel(t *testing.T, model Model, width, height int) Model {
	t.Helper()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func assertViewFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	if len(lines) > height {
		t.Fatalf("view is %d lines tall in a %d-line terminal:\n%s", len(lines), height, view)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d is %d cells wide in a %d-column terminal:\n%s", index+1, got, width, view)
		}
	}
}

func TestModelNavigationFilteringPreviewAndFormatCycle(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "fixture", Score: 2},
		{Filename: "Example-Bold.ttf", Format: "ttf", Source: "fixture", Score: 1},
	}, nil, false)
	if !strings.Contains(model.View(), "1 options  2 files") {
		t.Fatal(model.View())
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(Model)
	if model.format != "otf" || len(model.visible) != 1 {
		t.Fatalf("format=%s visible=%d", model.format, len(model.visible))
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.screen != screenPreview || !strings.Contains(model.View(), "Example-Regular.otf") {
		t.Fatal("preview did not open")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.screen == screenPreview {
		t.Fatal("preview did not close")
	}
}

func TestModelViewIncludesMonaMascot(t *testing.T) {
	t.Parallel()

	view := NewModel(nil, nil, false).View()
	if !strings.Contains(view, "(´∀｀)  文字  moji") {
		t.Fatalf("Mona mascot missing from view:\n%s", view)
	}
}

func TestHomeUsesFullMonaAndBlockWordmark(t *testing.T) {
	t.Parallel()
	for _, size := range []struct{ width, height int }{{100, 30}, {40, 16}} {
		model := sizedModel(t, NewHomeModel(nil, nil, false, "", rank.DefaultWeights(), 10, ""), size.width, size.height)
		view := model.View()
		assertViewFits(t, view, size.width, size.height)
		for _, wanted := range []string{"（　・∀・）", "█▀▄▀█ █▀█   █ █", "█ ▀ █ █▄█ █▄█ █"} {
			if !strings.Contains(view, wanted) {
				t.Fatalf("%dx%d home branding is missing %q:\n%s", size.width, size.height, wanted, view)
			}
		}
		if strings.Contains(view, "Mona finds the right font") {
			t.Fatalf("redundant mascot tagline remains:\n%s", view)
		}
	}
}

func TestViewsFitSupportedTerminalSizes(t *testing.T) {
	t.Parallel()
	results := make([]provider.Result, 12)
	for index := range results {
		results[index] = provider.Result{
			Filename: "SourceSans3VF-UltraLightItalic-With-A-Very-Long-Name.woff2",
			Format:   "woff2", Weight: "extra-light", Source: "getfonts.cc/a-provider-with-a-long-name",
			URL: "https://example.test/fonts/SourceSans3VF-UltraLightItalic-With-A-Very-Long-Name.woff2?download=1",
		}
	}

	for _, size := range []struct{ width, height int }{{120, 30}, {80, 24}, {60, 18}, {40, 12}, {24, 8}} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := sizedModel(t, NewModel(results, nil, false), size.width, size.height)
			assertViewFits(t, model.View(), size.width, size.height)

			model.screen = screenPreview
			assertViewFits(t, model.View(), size.width, size.height)

			home := sizedModel(t, NewHomeModel(nil, nil, false, "", rank.DefaultWeights(), 10, ""), size.width, size.height)
			assertViewFits(t, home.View(), size.width, size.height)
		})
	}
}

func TestZeroSizedPTYUsesFallbackDimensions(t *testing.T) {
	t.Parallel()
	model := sizedModel(t, NewModel([]provider.Result{{Filename: "Fallback.otf", Format: "otf"}}, nil, false), 0, 0)
	view := model.View()
	if !strings.Contains(view, "Fallback") || strings.Count(view, "\n") < 5 {
		t.Fatalf("zero-sized PTY collapsed the interface:\n%s", view)
	}
}

func TestResultsKeepChromeVisibleAndScrollSelectionIntoView(t *testing.T) {
	t.Parallel()
	results := make([]provider.Result, 20)
	for index := range results {
		results[index] = provider.Result{Filename: fmt.Sprintf("Font-%02d.otf", index), Format: "otf", Source: "fixture"}
	}
	model := sizedModel(t, NewModel(results, nil, false), 80, 12)
	for range 15 {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	view := model.View()
	assertViewFits(t, view, 80, 12)
	for _, wanted := range []string{"文字  moji", "20 options  20 files", "Font 15", "j/k"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("%q is not visible after scrolling:\n%s", wanted, view)
		}
	}
}

func TestResultsSupportPageAndBoundaryNavigation(t *testing.T) {
	t.Parallel()
	results := make([]provider.Result, 30)
	for index := range results {
		results[index] = provider.Result{Filename: fmt.Sprintf("Font-%02d.otf", index), Format: "otf", Score: float64(30 - index)}
	}
	model := sizedModel(t, NewModel(results, nil, false), 80, 12)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(Model)
	if model.resultsWindow.cursor <= 1 {
		t.Fatalf("page down only moved to result %d", model.resultsWindow.cursor)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if model.resultsWindow.cursor != len(results)-1 || !strings.Contains(model.View(), "Font 29") {
		t.Fatalf("end did not select the last result: cursor=%d\n%s", model.resultsWindow.cursor, model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.resultsWindow.cursor != 0 {
		t.Fatalf("home selected result %d", model.resultsWindow.cursor)
	}
}

func TestAlternateSortModesReorderFamilyCandidates(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Weight: "regular", Source: "getfonts.cc/owner/alpha", Provider: "getfonts"},
		{Filename: "Beta-Regular.woff2", Format: "woff2", Weight: "regular", Source: "github.com/owner/beta", Provider: "github"},
		{Filename: "Beta-Bold.woff2", Format: "woff2", Weight: "bold", Source: "github.com/owner/beta", Provider: "github"},
		{Filename: "Beta-Italic.woff2", Format: "woff2", Weight: "regular", Source: "github.com/owner/beta", Provider: "github"},
	}, nil, false)

	model.sortMode = 1
	model.refresh()
	if model.groups[0].FileCount != 3 {
		t.Fatalf("most-files order starts with %d files", model.groups[0].FileCount)
	}

	model.sortMode = 2
	model.refresh()
	if model.groups[0].BestFormat != "otf" {
		t.Fatalf("preferred-format order starts with %q", model.groups[0].BestFormat)
	}
}

func TestPreferredFormatOrderCoversEveryAcceptedFormat(t *testing.T) {
	formats := []string{"otf", "ttf", "dfont", "pfb", "woff2", "woff", "pfm"}
	results := make([]provider.Result, 0, len(formats))
	for index := len(formats) - 1; index >= 0; index-- {
		format := formats[index]
		results = append(results, provider.Result{
			Filename: fmt.Sprintf("Family%d.%s", index, format), Format: format,
			Source: fmt.Sprintf("fixture/%d", index), Provider: "fixture",
		})
	}
	model := NewModel(results, nil, false)
	model.sortMode = 2
	model.refresh()
	for index, format := range formats {
		if model.groups[index].BestFormat != format {
			t.Fatalf("preferred format %d = %q, want %q", index, model.groups[index].BestFormat, format)
		}
	}
}

func TestFilterMatchesDisplayedProvider(t *testing.T) {
	model := NewModel([]provider.Result{{
		Filename: "Example-Regular.otf", Format: "otf", Source: "fontsource.org", Provider: "registry",
	}}, nil, false)
	model.filter = "registry"
	model.refresh()
	if len(model.visible) != 1 || !strings.Contains(model.View(), "[registry]") {
		t.Fatalf("provider filter hid the visibly matching candidate:\n%s", model.View())
	}
}

func TestResponsiveHelpFitsWithoutLosingCommandsMidLabel(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 40, 60, 80, 120} {
		model := sizedModel(t, NewModel(nil, nil, false), width, 12)
		if got := lipgloss.Width(model.resultsHelp()); got > model.contentWidth() {
			t.Fatalf("results help is %d cells in %d available cells", got, model.contentWidth())
		}
		if got := lipgloss.Width(model.detailHelp()); got > model.contentWidth() {
			t.Fatalf("detail help is %d cells in %d available cells", got, model.contentWidth())
		}
		if got := lipgloss.Width(model.familyPreviewHelp()); got > model.contentWidth() {
			t.Fatalf("family preview help is %d cells in %d available cells", got, model.contentWidth())
		}
		if got := lipgloss.Width(model.healthHelp()); got > model.contentWidth() {
			t.Fatalf("health help is %d cells in %d available cells", got, model.contentWidth())
		}
		if got := lipgloss.Width(model.confirmHelp()); got > model.contentWidth() {
			t.Fatalf("confirm help is %d cells in %d available cells", got, model.contentWidth())
		}
	}
}

func TestLongDetailValuesWrapInsteadOfClipping(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{{
		Filename: "SourceSans3VF-UltraLightItalic.woff2", Format: "woff2", Weight: "ultralight",
		Source: "fixture", License: "OFL-1.1", URL: "https://example.test/a/very/long/path/to/a/font/file.woff2?download=1",
	}}, nil, false)
	model.screen = screenPreview
	model = sizedModel(t, model, 40, 14)
	view := model.View()
	assertViewFits(t, view, 40, 14)
	compact := strings.ReplaceAll(strings.ReplaceAll(view, "\n", ""), " ", "")
	if !strings.Contains(compact, "download=1") {
		t.Fatalf("wrapped URL lost its tail:\n%s", view)
	}
}

func TestLongDetailsScrollWithoutChangingSelectedResult(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{
		{Filename: "First.otf", Format: "otf", URL: "https://example.test/" + strings.Repeat("long-path/", 12) + "first.otf"},
		{Filename: "Second.otf", Format: "otf", URL: "https://example.test/second.otf"},
	}, nil, false)
	model.screen = screenPreview
	model = sizedModel(t, model, 40, 10)
	initial := model.View()
	for range 10 {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	view := model.View()
	assertViewFits(t, view, 40, 10)
	if model.resultsWindow.cursor != 0 {
		t.Fatalf("detail scrolling changed the selected result to %d", model.resultsWindow.cursor)
	}
	if view == initial || !strings.Contains(view, "first.otf") {
		t.Fatalf("detail viewport did not reveal the URL tail:\n%s", view)
	}
}

func TestSingleFontPreviewScrollsTheSelectedGroup(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Source: "fixture"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Source: "fixture"},
		{Filename: "Beta-Regular.otf", Format: "otf", Source: "fixture", URL: "https://example.test/" + strings.Repeat("beta-path/", 12) + "tail.otf"},
	}, nil, false)
	model.resultsWindow.cursor = 1
	model.screen = screenPreview
	model = sizedModel(t, model, 40, 10)
	for range 20 {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	view := model.View()
	if model.detailOffset <= 1 || !strings.Contains(view, "tail.otf") {
		t.Fatalf("selected Beta details stopped at offset %d:\n%s", model.detailOffset, view)
	}
}

func TestPreviewKeysCannotRetargetTheSelectedGroup(t *testing.T) {
	t.Parallel()
	var downloaded string
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Source: "fixture"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Source: "fixture"},
		{Filename: "Beta-Regular.otf", Format: "otf", Source: "fixture"},
	}, func(result provider.Result) (string, error) {
		downloaded = result.Filename
		return "/tmp/" + result.Filename, nil
	}, false)
	model.resultsWindow.cursor = 1
	model.screen = screenPreview
	model.selectAllCurrentGroup()
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyPgUp}, {Type: tea.KeyPgDown}, {Type: tea.KeyHome}, {Type: tea.KeyEnd},
		{Type: tea.KeyRunes, Runes: []rune{'g'}}, {Type: tea.KeyRunes, Runes: []rune{'G'}},
		{Type: tea.KeyRunes, Runes: []rune{'f'}}, {Type: tea.KeyRunes, Runes: []rune{'o'}},
		{Type: tea.KeyRunes, Runes: []rune{'/'}}, {Type: tea.KeyEnter},
	} {
		updated, _ := model.Update(key)
		model = updated.(Model)
	}
	if model.resultsWindow.cursor != 1 || model.currentGroup().FamilyName != "beta" {
		t.Fatalf("preview retargeted cursor=%d group=%q", model.resultsWindow.cursor, model.currentGroup().FamilyName)
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("preview download command is nil")
	}
	model.Update(command())
	if downloaded != "Beta-Regular.otf" {
		t.Fatalf("downloaded %q, want previewed Beta-Regular.otf", downloaded)
	}
}

func TestRefreshClosesDetailsWhenSelectedResultDisappears(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{{Filename: "Only.ttf", Format: "ttf"}}, nil, false)
	model.screen = screenPreview

	model.format = "otf"
	model.refresh()
	if model.screen == screenPreview || len(model.visible) != 0 {
		t.Fatalf("screen=%v visible=%d, want closed preview with no results", model.screen, len(model.visible))
	}

	// Scrolling after the refresh must remain safe when the filtered list is empty.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.resultsWindow.cursor != 0 {
		t.Fatalf("empty result cursor moved to %d", model.resultsWindow.cursor)
	}
}

func TestRefreshClosesPreviewWhenItsGroupDisappearsButOthersRemain(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Source: "fixture/alpha"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Source: "fixture/alpha"},
		{Filename: "Beta-Regular.ttf", Format: "ttf", Source: "fixture/beta"},
		{Filename: "Beta-Bold.ttf", Format: "ttf", Source: "fixture/beta"},
	}, nil, false)
	model.resultsWindow.cursor = 1
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.currentGroup().FamilyName != "beta" {
		t.Fatalf("opened %q, want beta", model.currentGroup().FamilyName)
	}

	model.format = "otf"
	model.refresh()
	if model.screen != screenResults || len(model.groups) != 1 || model.groups[0].FamilyName != "alpha" {
		t.Fatalf("disappeared preview remained open: screen=%v groups=%#v", model.screen, model.groups)
	}
}

func TestHomeModelStartsLiveSearchFromTypedQuery(t *testing.T) {
	t.Parallel()

	events := make(chan provider.Event, 2)
	events <- provider.Event{Provider: "fixture", Type: provider.EventResult, Result: provider.Result{Filename: "Quicksand-Regular.otf", Format: "otf"}}
	events <- provider.Event{Provider: "fixture", Type: provider.EventStatus, Status: provider.StateDone, Count: 1}
	close(events)
	var searched string
	model := NewHomeModel(func(query string) (<-chan provider.Event, error) {
		searched = query
		return events, nil
	}, nil, false, "", rank.DefaultWeights(), 10, "")
	if !strings.Contains(model.View(), "Type a font name") {
		t.Fatalf("home prompt missing:\n%s", model.View())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("uicksand")})
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if searched != "quicksand" || model.screen == screenHome || !model.loading || command == nil {
		t.Fatalf("searched=%q screen=%v loading=%v command=%v", searched, model.screen, model.loading, command != nil)
	}
	for command != nil {
		updated, command = model.Update(command())
		model = updated.(Model)
	}
	if !strings.Contains(model.View(), "Quicksand") {
		t.Fatalf("search result missing:\n%s", model.View())
	}
}

func TestHomeModelKeepsPastedQueryOnOneLine(t *testing.T) {
	model := sizedModel(t, NewHomeModel(nil, nil, false, "", rank.DefaultWeights(), 10, ""), 40, 12)

	updated, _ := model.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("Noto\nSans\tJP"),
		Paste: true,
	})
	model = updated.(Model)

	if model.query != "Noto Sans JP" {
		t.Fatalf("query = %q, want a single-line pasted query", model.query)
	}
	if got := len(strings.Split(model.View(), "\n")); got != 12 {
		t.Fatalf("rendered lines = %d, want terminal height 12", got)
	}
}

func TestHomeModelShowsGitHubConfigurationHint(t *testing.T) {
	t.Parallel()
	model := sizedModel(t, NewHomeModel(
		nil, nil, false, "", rank.DefaultWeights(), 10,
		"GitHub search is off. Set GITHUB_TOKEN to search more repositories.",
	), 96, 30)
	view := model.View()
	if !strings.Contains(view, "[!] GitHub search is off") || !strings.Contains(view, "GITHUB_TOKEN") {
		t.Fatalf("GitHub hint missing:\n%s", view)
	}
}

func TestModelDownloadCommandReportsStatus(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result) (string, error) {
		return "/tmp/" + result.Filename, nil
	}, false)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("download command was not returned")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if !strings.Contains(model.status, "Downloaded 1 file(s): /tmp/Example.otf") {
		t.Fatal(model.status)
	}
}

func TestGroupedResultsOpenSelectableFamilyPreview(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture"},
		{Filename: "Example-Bold.otf", Format: "otf", Weight: "bold", Source: "fixture"},
	}, nil, false)
	if len(model.groups) != 1 || model.groups[0].FileCount != 2 {
		t.Fatalf("groups = %#v", model.groups)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.screen != screenPreview || !strings.Contains(model.View(), "2/2 selected") {
		t.Fatalf("family preview missing:\n%s", model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	if model.selectedCount() != 1 || !strings.Contains(model.View(), "1/2 selected") {
		t.Fatalf("selection did not toggle:\n%s", model.View())
	}
}

func TestFamilyPreviewSelectsOneRankedFilePerStyle(t *testing.T) {
	results := []provider.Result{
		{Filename: "Example-Regular.ttf", Format: "ttf", Weight: "regular", Source: "fixture"},
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture"},
		{Filename: "Example-Italic.ttf", Format: "ttf", Weight: "regular", Source: "fixture"},
		{Filename: "Example-Italic.otf", Format: "otf", Weight: "regular", Source: "fixture"},
		{Filename: "Example-Bold.ttf", Format: "ttf", Weight: "bold", Source: "fixture"},
		{Filename: "Example-Bold.otf", Format: "otf", Weight: "bold", Source: "fixture"},
		{Filename: "Example-BoldItalic.ttf", Format: "ttf", Weight: "bold", Source: "fixture"},
		{Filename: "Example-BoldItalic.otf", Format: "otf", Weight: "bold", Source: "fixture"},
		{Filename: "Example[wdth,wght].ttf", Format: "ttf", Variable: true, Source: "fixture"},
		{Filename: "Example[wdth,wght].otf", Format: "otf", Variable: true, Source: "fixture"},
		{Filename: "Example-Italic[wdth,wght].ttf", Format: "ttf", Variable: true, Source: "fixture"},
		{Filename: "Example-Italic[wdth,wght].otf", Format: "otf", Variable: true, Source: "fixture"},
	}
	model := NewModel(results, nil, false)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.selectedCount() != 6 || !strings.Contains(model.View(), "6/12 selected") {
		t.Fatalf("default family selection chose %d files, want one per style:\n%s", model.selectedCount(), model.View())
	}
	for index, result := range model.currentGroup().Files {
		if model.selectedFiles[index] && result.Format != "otf" {
			t.Fatalf("selected lower-ranked duplicate %q", result.Filename)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(Model)
	if model.selectedCount() != len(results) {
		t.Fatalf("select all chose %d/%d files", model.selectedCount(), len(results))
	}
}

func TestLiveResultsKeepTheOpenedFamilyAndSelectionsStable(t *testing.T) {
	var downloaded []string
	model := NewModel([]provider.Result{
		{Filename: "Beta-Regular.ttf", Format: "ttf", Weight: "regular", Source: "fixture/beta", URL: "https://example.test/beta-regular.ttf"},
		{Filename: "Beta-Bold.ttf", Format: "ttf", Weight: "bold", Source: "fixture/beta", URL: "https://example.test/beta-bold.ttf"},
	}, func(result provider.Result) (string, error) {
		downloaded = append(downloaded, result.Filename)
		return "/tmp/" + result.Filename, nil
	}, false)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	for _, result := range []provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture/alpha", URL: "https://example.test/alpha-regular.otf"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Weight: "bold", Source: "fixture/alpha", URL: "https://example.test/alpha-bold.otf"},
	} {
		updated, _ = model.Update(eventMessage{event: provider.Event{Provider: "fixture", Type: provider.EventResult, Result: result}, open: true})
		model = updated.(Model)
	}
	if model.currentGroup().FamilyName != "beta" || model.selectedCount() != 2 {
		t.Fatalf("live ranking retargeted preview to %q with %d selected", model.currentGroup().FamilyName, model.selectedCount())
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("download command is nil")
	}
	model.Update(command())
	if strings.Join(downloaded, ",") != "Beta-Bold.ttf,Beta-Regular.ttf" {
		t.Fatalf("downloaded retargeted files: %v", downloaded)
	}
}

func TestLiveReorderingPreservesManualFileToggles(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.ttf", Format: "ttf", Weight: "regular", Source: "fixture", URL: "https://example.test/regular.ttf"},
		{Filename: "Example-Bold.ttf", Format: "ttf", Weight: "bold", Source: "fixture", URL: "https://example.test/bold.ttf"},
	}, nil, false)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	deselected := provider.ResultIdentity(model.currentGroup().Files[model.previewWindow.cursor])
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	selected := make(map[string]bool)
	for index, result := range model.currentGroup().Files {
		if model.selectedFiles[index] {
			selected[provider.ResultIdentity(result)] = true
		}
	}

	updated, _ = model.Update(eventMessage{event: provider.Event{Provider: "fixture", Type: provider.EventResult, Result: provider.Result{
		Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture", URL: "https://example.test/regular.otf",
	}}, open: true})
	model = updated.(Model)
	for index, result := range model.currentGroup().Files {
		identity := provider.ResultIdentity(result)
		if identity == deselected && model.selectedFiles[index] {
			t.Fatalf("manually deselected %q became selected", result.Filename)
		}
		if selected[identity] && !model.selectedFiles[index] {
			t.Fatalf("selected %q became deselected", result.Filename)
		}
	}
}

func TestLiveResultsCannotRetargetAConfirmedFamilyDownload(t *testing.T) {
	var downloaded []string
	model := NewModel([]provider.Result{
		{Filename: "Beta-Regular.ttf", Format: "ttf", Weight: "regular", Source: "fixture/beta", URL: "https://example.test/beta-regular.ttf"},
		{Filename: "Beta-Medium.ttf", Format: "ttf", Weight: "medium", Source: "fixture/beta", URL: "https://example.test/beta-medium.ttf"},
		{Filename: "Beta-Bold.ttf", Format: "ttf", Weight: "bold", Source: "fixture/beta", URL: "https://example.test/beta-bold.ttf"},
		{Filename: "Beta-Black.ttf", Format: "ttf", Weight: "black", Source: "fixture/beta", URL: "https://example.test/beta-black.ttf"},
	}, func(result provider.Result) (string, error) {
		downloaded = append(downloaded, result.Filename)
		return "/tmp/" + result.Filename, nil
	}, false)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if model.screen != screenConfirm || command != nil {
		t.Fatalf("large family did not open confirmation: screen=%v", model.screen)
	}

	for _, result := range []provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture/alpha", URL: "https://example.test/alpha-regular.otf"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Weight: "bold", Source: "fixture/alpha", URL: "https://example.test/alpha-bold.otf"},
	} {
		updated, _ = model.Update(eventMessage{event: provider.Event{Provider: "fixture", Type: provider.EventResult, Result: result}, open: true})
		model = updated.(Model)
	}
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("confirmed download command is nil")
	}
	model.Update(command())
	if len(downloaded) != 4 {
		t.Fatalf("downloaded %d files, want 4 Beta styles: %v", len(downloaded), downloaded)
	}
	for _, filename := range downloaded {
		if !strings.HasPrefix(filename, "Beta-") {
			t.Fatalf("confirmation downloaded unreviewed file %q", filename)
		}
	}
}

func TestGroupedResultUsesTheResponsiveRowShape(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "github"},
		{Filename: "Example-Bold.otf", Format: "otf", Source: "github"},
	}, nil, false)
	for _, test := range []struct {
		width, wantRows int
	}{{24, 2}, {40, 2}, {60, 1}, {80, 1}, {120, 1}} {
		model := sizedModel(t, model, test.width, 20)
		rows := model.groupRow(model.groups[0], true)
		if len(rows) != test.wantRows || !strings.Contains(rows[0], "Example") {
			t.Fatalf("%d-column group rows = %#v, want %d", test.width, rows, test.wantRows)
		}
	}
}

func TestStackedCandidateMetadataUsesFixedColumns(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Source: "github.com/owner/alpha", Provider: "github"},
		{Filename: "Beta-Regular.woff2", Format: "woff2", Source: "github.com/owner/beta", Provider: "github"},
	}, nil, false)
	model = sizedModel(t, model, 40, 16)
	view := model.View()
	var providerColumns []int
	for _, line := range strings.Split(view, "\n") {
		if column := strings.Index(line, "[github]"); column >= 0 && !strings.Contains(line, "Best") {
			providerColumns = append(providerColumns, column)
		}
	}
	if len(providerColumns) != 2 || providerColumns[0] != providerColumns[1] {
		t.Fatalf("stacked provider columns = %v, want two aligned columns:\n%s", providerColumns, view)
	}
}

func TestResultGridShowsOnlyAlignedFamilyCandidates(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "GaramondPremierPro-Regular.ttf", Format: "ttf", Source: "github.com/owner/family", Provider: "github", Score: 12.3},
		{Filename: "GaramondPremierPro-Bold.ttf", Format: "ttf", Source: "github.com/owner/family", Provider: "github", Score: 12.3},
		{Filename: "GaramondPremierProCaption.otf", Format: "otf", Source: "getfonts.cc/archive/fonts", Provider: "getfonts", Score: 9.2},
	}, nil, false)
	model.query = "Garamond Premier Pro"
	model = sizedModel(t, model, 120, 20)
	view := model.View()
	assertViewFits(t, view, 120, 20)

	for _, wanted := range []string{"Query", "Garamond Premier Pro", "Recommended", "Family", "Files", "Format", "Provider", "[github]", "[getfonts]"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("results grid is missing %q:\n%s", wanted, view)
		}
	}
	for _, unwanted := range []string{"Family / file", "> +", "  -", "GaramondPremierProCaption.otf", "owner/family", "archive/fonts", "Score"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("results grid still exposes %q:\n%s", unwanted, view)
		}
	}

	lines := strings.Split(view, "\n")
	var heading string
	var candidates []string
	for _, line := range lines {
		switch {
		case strings.Contains(line, "Family") && strings.Contains(line, "Provider"):
			heading = line
		case strings.Contains(line, "[github]"), strings.Contains(line, "[getfonts]"):
			if !strings.Contains(line, "Recommended") {
				candidates = append(candidates, line)
			}
		}
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate rows = %d, want 2:\n%s", len(candidates), view)
	}
	for _, column := range []string{"Format", "Provider"} {
		position := strings.Index(heading, column)
		for _, candidate := range candidates {
			if position < 0 || position >= len(candidate) || candidate[position] == ' ' {
				t.Fatalf("column %q was not rendered consistently:\n%s", column, view)
			}
		}
	}
}

func TestFamilyPreviewUsesAlignedFileColumns(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "github.com/owner/fonts", Provider: "github"},
		{Filename: "Example-ExtraBoldItalic.woff2", Format: "woff2", Weight: "bold", Source: "github.com/owner/fonts", Provider: "github"},
	}, nil, false)
	model.screen = screenPreview
	model.selectAllCurrentGroup()
	model = sizedModel(t, model, 80, 18)
	view := model.View()
	assertViewFits(t, view, 80, 18)

	lines := strings.Split(view, "\n")
	var heading string
	var files []string
	for _, line := range lines {
		switch {
		case strings.Contains(line, "File") && strings.Contains(line, "Weight"):
			heading = line
		case strings.Contains(line, "Example-"):
			files = append(files, line)
		}
	}
	if len(files) != 2 {
		t.Fatalf("preview rows = %d, want 2:\n%s", len(files), view)
	}
	for _, column := range []string{"Format", "Weight"} {
		position := strings.Index(heading, column)
		for _, file := range files {
			if position < 0 || position >= len(file) || file[position] == ' ' {
				t.Fatalf("preview column %q is misaligned:\n%s", column, view)
			}
		}
	}
}

func TestVeryShortFamilyPreviewKeepsFileMetadata(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "GaramondPremierPro-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture", Provider: "fixture"},
		{Filename: "GaramondPremierPro-Roman.otf", Format: "otf", Weight: "regular", Source: "fixture", Provider: "fixture"},
	}, nil, false)
	model.screen = screenPreview
	model.selectAllCurrentGroup()
	model = sizedModel(t, model, 24, 7)
	view := model.View()
	assertViewFits(t, view, 24, 7)
	for _, wanted := range []string{"Gar...", "OTF", "regular"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("short preview lost %q:\n%s", wanted, view)
		}
	}
}

func TestResultsHierarchyHidesSettledProviderNoise(t *testing.T) {
	model := NewModel([]provider.Result{{Filename: "GaramondPremierPro.otf", Format: "otf", Source: "github.com/owner/fonts"}}, nil, false)
	model.query = "Garamond Premier Pro"
	model.providerStatus["github"] = "done (214 results)"
	model.providerStatus["registry"] = "done (0 results)"
	model = sizedModel(t, model, 80, 16)

	view := model.View()
	for _, unwanted := range []string{"done (214 results)", "done (0 results)", "format:all", "sort:score"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("settled results contain %q:\n%s", unwanted, view)
		}
	}
	for _, wanted := range []string{"Query", "Garamond Premier Pro", "Format All", "Order Best match", "Recommended", "1/1"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("results hierarchy is missing %q:\n%s", wanted, view)
		}
	}

	model.screen = screenHealth
	if health := model.View(); !strings.Contains(health, "done (214 results)") || !strings.Contains(health, "done (0 results)") {
		t.Fatalf("provider details disappeared from Health:\n%s", health)
	}
}

func TestResultsLoadingAndProviderAttentionStayConcise(t *testing.T) {
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, nil, false)
	model.loading = true
	model.providerStatus["github"] = "searching"
	model.providerStatus["getfonts"] = "done (2 results)"
	model.providerStates["github"] = provider.StateSearching
	model.providerStates["getfonts"] = provider.StateDone
	model = sizedModel(t, model, 80, 16)
	if view := model.View(); !strings.Contains(view, "Searching providers") || strings.Contains(view, "getfonts: done") {
		t.Fatalf("loading progress is not concise:\n%s", view)
	}
	if view := model.View(); strings.Contains(view, "Recommended") || !strings.Contains(view, "Best so far") {
		t.Fatalf("loading results were presented as a settled recommendation:\n%s", view)
	}
	model.providerStates["github"] = provider.StateFailed
	model.providerStates["getfonts"] = provider.StateSearching
	if view := model.View(); !strings.Contains(view, "1/2 finished") {
		t.Fatalf("terminal provider failures were not counted as finished:\n%s", view)
	}
	compact := sizedModel(t, model, 24, 8)
	if view := compact.View(); !strings.Contains(view, "Example") || !strings.Contains(view, "OTF") {
		t.Fatalf("compact loading chrome displaced the two-line result:\n%s", view)
	}

	model.loading = false
	model.providerStatus["github"] = "rate limited"
	model.providerStates["github"] = provider.StateThrottled
	if view := model.View(); !strings.Contains(view, "provider needs attention") || strings.Contains(view, "github: rate limited") {
		t.Fatalf("provider attention is not summarized:\n%s", view)
	}
}

func TestProviderDisplayNeverIncludesOrigin(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "github.com/owner/private-fonts", Provider: "github"},
		{Filename: "Example-Bold.otf", Format: "otf", Source: "github.com/owner/private-fonts", Provider: "github"},
	}, nil, false)
	model = sizedModel(t, model, 80, 16)
	view := model.View()
	if !strings.Contains(view, "[github]") || strings.Contains(view, "owner/private-fonts") || strings.Contains(view, "github.com") {
		t.Fatalf("provider display leaked source provenance:\n%s", view)
	}
}

func TestResultRowsAndFooterFillTheCenteredColumn(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "github.com/owner/fonts"},
		{Filename: "Example-Bold.otf", Format: "otf", Source: "github.com/owner/fonts"},
	}, nil, false)
	for _, size := range []struct{ width, height, content int }{
		{24, 8, 24}, {40, 12, 36}, {60, 16, 56}, {80, 20, 72}, {120, 24, 112}, {154, 30, 118},
	} {
		model := sizedModel(t, model, size.width, size.height)
		if model.contentWidth() != size.content {
			t.Fatalf("%d columns produced content width %d, want %d", size.width, model.contentWidth(), size.content)
		}
		rows := model.groupRow(model.groups[0], true)
		for _, row := range rows {
			if got := lipgloss.Width(row); got != model.contentWidth() {
				t.Fatalf("selected row is %d cells, want %d at terminal width %d: %q", got, model.contentWidth(), size.width, row)
			}
		}
		footer := strings.Split(model.resultsHelp(), "\n")[0]
		if got := lipgloss.Width(footer); got != model.contentWidth() || !strings.Contains(footer, "1/1") {
			t.Fatalf("footer at %d columns = %q (%d cells)", size.width, footer, got)
		}
	}
}

func TestVeryNarrowChromePrioritizesContext(t *testing.T) {
	model := sizedModel(t, NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, nil, false), 24, 8)
	view := model.View()
	assertViewFits(t, view, 24, 8)
	if !strings.Contains(view, "moji  results") || strings.Contains(view, "文字") {
		t.Fatalf("narrow header did not prioritize context:\n%s", view)
	}
}

func TestLargeFamilyDownloadRequiresConfirmation(t *testing.T) {
	t.Parallel()
	results := make([]provider.Result, 4)
	for index, weight := range []string{"Regular", "Light", "Bold", "Black"} {
		results[index] = provider.Result{Filename: "Example-" + weight + ".otf", Format: "otf", Source: "fixture"}
	}
	model := NewModel(results, func(result provider.Result) (string, error) { return "/tmp/" + result.Filename, nil }, false)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if model.screen != screenConfirm || command != nil || !strings.Contains(model.View(), "Download 4 selected files?") {
		t.Fatalf("confirmation missing:\n%s", model.View())
	}
}

func TestProviderHealthScreenUsesStreamedStatuses(t *testing.T) {
	t.Parallel()
	model := NewModel(nil, nil, false)
	model.providerStatus["github"] = "rate limited"
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	model = updated.(Model)
	if model.screen != screenHealth || !strings.Contains(model.View(), "github") || !strings.Contains(model.View(), "rate limited") {
		t.Fatalf("provider health missing:\n%s", model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.screen != screenResults {
		t.Fatalf("tab returned to screen %v, want results", model.screen)
	}
}

func TestResultsReserveAVisibleRowForStatus(t *testing.T) {
	results := make([]provider.Result, 20)
	for index := range results {
		results[index] = provider.Result{Filename: fmt.Sprintf("Font-%02d.otf", index), Format: "otf"}
	}
	model := sizedModel(t, NewModel(results, nil, false), 80, 12)
	model.resultsWindow.cursor = 15
	model.status = "Downloaded: /tmp/Font-15.otf"

	view := model.View()
	if !strings.Contains(view, "> Font 15") {
		t.Fatalf("selected result disappeared behind status:\n%s", view)
	}
	if !strings.Contains(view, model.status) {
		t.Fatalf("status missing from view:\n%s", view)
	}
}

func TestCompactResultsKeepCompleteMetadataBesideStatus(t *testing.T) {
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "fixture"},
		{Filename: "Example-Bold.otf", Format: "otf", Source: "fixture"},
	}, nil, false)
	model.status = "Downloaded: /tmp/Example-Regular.otf"
	model = sizedModel(t, model, 24, 8)

	view := model.View()
	assertViewFits(t, view, 24, 8)
	for _, wanted := range []string{"Example", "OTF", "Downloaded"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("compact status view lost %q:\n%s", wanted, view)
		}
	}

	minimal := sizedModel(t, model, 24, 7)
	minimalView := minimal.View()
	assertViewFits(t, minimalView, 24, 7)
	if !strings.Contains(minimalView, "Example") || !strings.Contains(minimalView, "Downloaded") {
		t.Fatalf("one-line compact fallback lost the result or status:\n%s", minimalView)
	}
}

func TestLiveModelStreamsProviderEvents(t *testing.T) {
	t.Parallel()
	events := make(chan provider.Event, 3)
	events <- provider.Event{Provider: "fixture", Type: provider.EventStatus, Status: provider.StateSearching}
	events <- provider.Event{Provider: "fixture", Type: provider.EventResult, Result: provider.Result{Filename: "Live.otf", Format: "otf"}}
	events <- provider.Event{Provider: "fixture", Type: provider.EventStatus, Status: provider.StateDone, Count: 1}
	close(events)
	model := NewLiveModel(events, nil, false, "", "", rank.DefaultWeights(), 10)
	for command := model.Init(); command != nil; {
		updated, next := model.Update(command())
		model = updated.(Model)
		command = next
	}
	if model.loading || len(model.visible) != 1 || !strings.Contains(model.View(), "Live") {
		t.Fatalf("live model did not settle: %s", model.View())
	}
	model.screen = screenHealth
	if !strings.Contains(model.View(), "done (1 results)") {
		t.Fatalf("settled provider state is missing from Health: %s", model.View())
	}
}

func TestLiveModelHonorsExplicitMaximum(t *testing.T) {
	t.Parallel()
	events := make(chan provider.Event, 13)
	for index := range 12 {
		events <- provider.Event{Provider: "fixture", Type: provider.EventResult, Result: provider.Result{
			Filename: fmt.Sprintf("Font-%02d.otf", index), Format: "otf", URL: fmt.Sprintf("https://example.test/%02d", index),
		}}
	}
	events <- provider.Event{Provider: "fixture", Type: provider.EventStatus, Status: provider.StateDone, Count: 12}
	close(events)
	model := NewLiveModel(events, nil, false, "", "", rank.DefaultWeights(), 5)
	for command := model.Init(); command != nil; {
		updated, next := model.Update(command())
		model = updated.(Model)
		command = next
	}
	if len(model.visible) != 5 {
		t.Fatalf("explicit maximum yielded %d results, want 5", len(model.visible))
	}
}
