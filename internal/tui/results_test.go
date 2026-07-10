package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
)

func TestModelNavigationFilteringPreviewAndFormatCycle(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Source: "fixture", Score: 2},
		{Filename: "Example-Bold.ttf", Format: "ttf", Source: "fixture", Score: 1},
	}, nil, false)
	if !strings.Contains(model.View(), "Found 2 results") {
		t.Fatal(model.View())
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(Model)
	if model.format != "otf" || len(model.visible) != 1 {
		t.Fatalf("format=%s visible=%d", model.format, len(model.visible))
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.preview || !strings.Contains(model.View(), "Example-Regular.otf") {
		t.Fatal("preview did not open")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.preview {
		t.Fatal("preview did not close")
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
	if !strings.Contains(model.status, "Downloaded: /tmp/Example.otf") {
		t.Fatal(model.status)
	}
}

func TestLiveModelStreamsProviderEvents(t *testing.T) {
	t.Parallel()
	events := make(chan provider.Event, 3)
	events <- provider.Event{Provider: "fixture", Type: provider.EventStatus, Status: provider.StateSearching}
	events <- provider.Event{Provider: "fixture", Type: provider.EventResult, Result: provider.Result{Filename: "Live.otf", Format: "otf"}}
	events <- provider.Event{Provider: "fixture", Type: provider.EventStatus, Status: provider.StateDone, Count: 1}
	close(events)
	model := NewLiveModel(events, nil, false, "", rank.DefaultWeights(), 10)
	for command := model.Init(); command != nil; {
		updated, next := model.Update(command())
		model = updated.(Model)
		command = next
	}
	if model.loading || len(model.visible) != 1 || !strings.Contains(model.View(), "fixture: done (1 results)") {
		t.Fatalf("live model did not settle: %s", model.View())
	}
}
