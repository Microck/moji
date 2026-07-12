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
	if !strings.Contains(model.View(), "Found 2 files in 1 groups") {
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
	if !strings.Contains(view, "Fallback.otf") || strings.Count(view, "\n") < 5 {
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
	for _, wanted := range []string{"文字  moji", "Found 20 files in 20 groups", "Font-15.otf", "j/k"} {
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
	if model.resultsWindow.cursor != len(results)-1 || !strings.Contains(model.View(), "Font-29.otf") {
		t.Fatalf("end did not select the last result: cursor=%d\n%s", model.resultsWindow.cursor, model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.resultsWindow.cursor != 0 {
		t.Fatalf("home selected result %d", model.resultsWindow.cursor)
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

func TestRefreshClosesDetailsWhenSelectedResultDisappears(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{{Filename: "Only.ttf", Format: "ttf"}}, nil, false)
	model.screen = screenPreview

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(Model)
	if model.screen == screenPreview || len(model.visible) != 0 {
		t.Fatalf("screen=%v visible=%d, want closed preview with no results", model.screen, len(model.visible))
	}

	// Scrolling after the refresh must remain safe when the filtered list is empty.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.resultsWindow.cursor != 0 {
		t.Fatalf("empty result cursor moved to %d", model.resultsWindow.cursor)
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
	if !strings.Contains(model.View(), "Quicksand-Regular.otf") {
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
	if !strings.Contains(view, "> Font-15.otf") {
		t.Fatalf("selected result disappeared behind status:\n%s", view)
	}
	if !strings.Contains(view, model.status) {
		t.Fatalf("status missing from view:\n%s", view)
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
	if model.loading || len(model.visible) != 1 || !strings.Contains(model.View(), "fixture: done (1 results)") {
		t.Fatalf("live model did not settle: %s", model.View())
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
