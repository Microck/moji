package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/microck/moji/internal/config"
)

func TestConfigModelEditsTogglesAndSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	model := NewConfigModel(config.Default(), path, false)
	model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	for range len([]rune(model.fields[0].value)) {
		model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/tmp/fonts")})
	model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	model.cursor = 5
	model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeySpace})
	model.fields[14].value = "9"
	model.fields[20].value = "21"
	model.fields[21].value = "4"
	model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !model.saved {
		t.Fatalf("config was not saved: %s", model.status)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.DownloadDir != "/tmp/fonts" || saved.Providers["github"].Enabled || saved.Ranking.Format != 9 ||
		saved.RateLimits["github"].TimeoutSeconds != 21 || saved.RateLimits["github"].Retries != 4 {
		t.Fatalf("saved config = %#v", saved)
	}
}

func TestConfigModelMasksTokenAndFitsTerminal(t *testing.T) {
	current := config.Default()
	current.GitHubToken = "ghp_secret_value"
	model := NewConfigModel(current, filepath.Join(t.TempDir(), "config.yaml"), false)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 48, Height: 12})
	view := updated.(ConfigModel).View()
	assertViewFits(t, view, 48, 12)
	if strings.Contains(view, current.GitHubToken) || !strings.Contains(view, "************") {
		t.Fatalf("token was exposed or not masked:\n%s", view)
	}
}

func TestConfigCompactLayoutPutsValuesBelowLabels(t *testing.T) {
	model := NewConfigModel(config.Default(), filepath.Join(t.TempDir(), "config.yaml"), false)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 16})
	view := updated.(ConfigModel).View()
	assertViewFits(t, view, 40, 16)
	if !strings.Contains(view, "> Download directory\n      /home/") {
		t.Fatalf("compact config did not give the value its own row:\n%s", view)
	}
}

func TestConfigMinimumWidthKeepsHeaderAndActionsVisible(t *testing.T) {
	model := NewConfigModel(config.Default(), filepath.Join(t.TempDir(), "config.yaml"), false)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 24, Height: 8})
	view := updated.(ConfigModel).View()
	assertViewFits(t, view, 24, 8)
	for _, wanted := range []string{"moji  config", "enter edit", "s", "q"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("minimum config omitted %q:\n%s", wanted, view)
		}
	}
}

func TestConfigModelRejectsInvalidValuesWithoutReplacingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := NewConfigModel(config.Default(), path, false)
	model.fields[2].value = "0"
	model = updateConfigModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if model.saved || string(content) != "sentinel" || !strings.Contains(model.status, "greater than 0") {
		t.Fatalf("saved=%v status=%q content=%q", model.saved, model.status, content)
	}
}

func updateConfigModel(t *testing.T, model ConfigModel, message tea.Msg) ConfigModel {
	t.Helper()
	updated, _ := model.Update(message)
	return updated.(ConfigModel)
}
