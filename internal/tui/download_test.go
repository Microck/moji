package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/microck/moji/internal/provider"
)

// runDownload executes the batched download command the way Bubble Tea would,
// skipping animation ticks, and returns the model after the download finished.
func runDownload(t *testing.T, model Model, command tea.Cmd) Model {
	t.Helper()
	if model.screen != screenDownloading {
		t.Fatalf("download did not open the progress screen: screen=%v", model.screen)
	}
	return runBackground(t, model, command, func(message tea.Msg) bool {
		_, ok := message.(downloadMessage)
		return ok
	})
}

// runInstall does the same for the install command.
func runInstall(t *testing.T, model Model, command tea.Cmd) Model {
	t.Helper()
	if model.installState != installRunning || command == nil {
		t.Fatalf("install did not start: state=%v command=%v", model.installState, command != nil)
	}
	return runBackground(t, model, command, func(message tea.Msg) bool {
		_, ok := message.(installMessage)
		return ok
	})
}

func runBackground(t *testing.T, model Model, command tea.Cmd, finished func(tea.Msg) bool) Model {
	t.Helper()
	pending := []tea.Cmd{command}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		switch message := next().(type) {
		case tea.BatchMsg:
			pending = append(pending, message...)
		case downloadTickMessage:
			// Ticks only animate the view; running them would loop forever.
		default:
			updated, follow := model.Update(message)
			model = updated.(Model)
			if finished(message) {
				return model
			}
			pending = append(pending, follow)
		}
	}
	t.Fatal("background command never finished")
	return model
}

func press(model Model, key string) (Model, tea.Cmd) {
	message := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		message = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		message = tea.KeyMsg{Type: tea.KeyEsc}
	}
	updated, command := model.Update(message)
	return updated.(Model), command
}

func TestDownloadShowsProgressScreen(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	reported := make(chan struct{})
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result, progress ProgressFunc) (string, error) {
		progress(512, 1024)
		close(reported)
		<-release
		return "/tmp/" + result.Filename, nil
	}, false)
	model, command := press(model, "D")
	if model.screen != screenDownloading || command == nil {
		t.Fatalf("screen=%v command=%v", model.screen, command != nil)
	}
	batch := command().(tea.BatchMsg)
	done := make(chan tea.Msg, 1)
	go func() { done <- batch[0]() }()
	<-reported
	view := model.View()
	for _, wanted := range []string{"Downloading file 1 of 1", "Example.otf", "50%", "512 B of 1.0 KB"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("progress view lost %q:\n%s", wanted, view)
		}
	}
	// Keys other than ctrl+c must not leave the progress screen.
	model, _ = press(model, "esc")
	if model.screen != screenDownloading {
		t.Fatalf("esc left the progress screen: %v", model.screen)
	}
	close(release)
	updated, _ := model.Update(<-done)
	model = updated.(Model)
	if model.screen != screenDownloadResult || !strings.Contains(model.View(), "Downloaded 1 file(s)") {
		t.Fatalf("download result not shown:\n%s", model.View())
	}
}

func TestDownloadErrorIsShownWithoutLeavingPreview(t *testing.T) {
	t.Parallel()
	attempts := 0
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Weight: "bold", Source: "fixture"},
	}, func(result provider.Result, _ ProgressFunc) (string, error) {
		attempts++
		// Fail the second file of each run so one file is saved first.
		if attempts%2 == 0 {
			return "", errors.New("fixture couldn't provide the font (HTTP 404)")
		}
		return "/tmp/" + result.Filename, nil
	}, false)
	model, _ = press(model, "enter")
	if model.screen != screenPreview {
		t.Fatalf("preview did not open: %v", model.screen)
	}
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	view := model.View()
	for _, wanted := range []string{"Download failed", "HTTP 404", "Alpha-", "Saved before the failure"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("error view lost %q:\n%s", wanted, view)
		}
	}
	model, command = press(model, "r")
	if command == nil || model.screen != screenDownloading {
		t.Fatalf("retry did not restart the download: screen=%v", model.screen)
	}
	model = runDownload(t, model, command)
	if attempts != 4 {
		t.Fatalf("retry made %d attempts, want 4", attempts)
	}
	model, _ = press(model, "esc")
	if model.screen != screenPreview {
		t.Fatalf("dismissing the error left preview: screen=%v", model.screen)
	}
}

func TestDownloadOffersToInstallFonts(t *testing.T) {
	t.Parallel()
	var installed []string
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func(paths []string, _ bool, _ InstallProgressFunc) ([]string, error) {
		installed = append(installed, paths...)
		return []string{"/fonts/Example.otf"}, nil
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	if model.installState != installPrompt || !strings.Contains(model.View(), "Copy 1 file(s) to /fonts") {
		t.Fatalf("install prompt missing:\n%s", model.View())
	}
	model, command = press(model, "y")
	model = runInstall(t, model, command)
	if strings.Join(installed, ",") != "/downloads/Example.otf" || !strings.Contains(model.View(), "Installed 1 file(s) to /fonts") {
		t.Fatalf("installed=%v view:\n%s", installed, model.View())
	}
	model, _ = press(model, "enter")
	if model.screen != screenResults {
		t.Fatalf("screen=%v, want results", model.screen)
	}
}

func TestDownloadInstallPromptCanBeSkipped(t *testing.T) {
	t.Parallel()
	called := false
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) {
		called = true
		return nil, nil
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	model, command = press(model, "n")
	if called || command != nil || model.screen != screenResults {
		t.Fatalf("skip installed=%v screen=%v", called, model.screen)
	}
}

func TestDownloadIntoFontDirSkipsInstallPrompt(t *testing.T) {
	t.Parallel()
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/fonts/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) { return nil, nil }})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	if model.installState != installNone {
		t.Fatalf("prompted to install files already in the font directory:\n%s", model.View())
	}
}

type fakeConflict struct{ names []string }

func (conflict fakeConflict) Error() string           { return "conflict" }
func (conflict fakeConflict) ConflictNames() []string { return conflict.names }

func TestInstallConflictPromptsToOverwrite(t *testing.T) {
	t.Parallel()
	var overwrites []bool
	model := NewModel([]provider.Result{{Filename: "dankmono.ttf", Format: "ttf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func(paths []string, overwrite bool, _ InstallProgressFunc) ([]string, error) {
		overwrites = append(overwrites, overwrite)
		if !overwrite {
			return nil, fakeConflict{names: []string{"dankmono.ttf"}}
		}
		return []string{"/fonts/dankmono.ttf"}, nil
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	model, command = press(model, "y")
	model = runInstall(t, model, command)
	if model.installState != installConflict || !strings.Contains(model.View(), "already has a different font named dankmono.ttf.") {
		t.Fatalf("overwrite prompt missing:\n%s", model.View())
	}
	model, command = press(model, "y")
	model = runInstall(t, model, command)
	if len(overwrites) != 2 || overwrites[0] || !overwrites[1] || !strings.Contains(model.View(), "Installed 1 file(s) to /fonts") {
		t.Fatalf("overwrites=%v view:\n%s", overwrites, model.View())
	}
}

func TestInstallConflictCanKeepExisting(t *testing.T) {
	t.Parallel()
	calls := 0
	model := NewModel([]provider.Result{{Filename: "dankmono.ttf", Format: "ttf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) {
		calls++
		return nil, fakeConflict{names: []string{"dankmono.ttf"}}
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	model, command = press(model, "y")
	model = runInstall(t, model, command)
	model, command = press(model, "n")
	if calls != 1 || command != nil || model.screen != screenResults || !strings.Contains(model.status, "Install skipped") {
		t.Fatalf("calls=%d screen=%v status=%q", calls, model.screen, model.status)
	}
}

func TestInstallPromptDefaultsToInstallAndConflictDefaultsToKeep(t *testing.T) {
	t.Parallel()
	var overwrites []bool
	model := NewModel([]provider.Result{{Filename: "dankmono.ttf", Format: "ttf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func(paths []string, overwrite bool, progress InstallProgressFunc) ([]string, error) {
		overwrites = append(overwrites, overwrite)
		progress(0, 1, false)
		return nil, fakeConflict{names: []string{"dankmono.ttf"}}
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	view := model.View()
	for _, wanted := range []string{"Install this font?", "[ Install ]", "Skip", "enter: confirm"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("install card lost %q:\n%s", wanted, view)
		}
	}
	// Enter takes the highlighted default, which is Install.
	model, command = press(model, "enter")
	model = runInstall(t, model, command)
	if model.installState != installConflict || !strings.Contains(model.View(), "[ Keep existing ]") {
		t.Fatalf("conflict did not default to keeping the installed font:\n%s", model.View())
	}
	// Enter on the conflict keeps the installed font.
	model, command = press(model, "enter")
	if command != nil || len(overwrites) != 1 || model.screen != screenResults {
		t.Fatalf("enter overwrote: overwrites=%v screen=%v", overwrites, model.screen)
	}
}

func TestInstallPromptArrowSwitchesChoice(t *testing.T) {
	t.Parallel()
	called := false
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) {
		called = true
		return nil, nil
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if !strings.Contains(model.View(), "[ Skip ]") {
		t.Fatalf("right arrow did not move to Skip:\n%s", model.View())
	}
	model, command = press(model, "enter")
	if called || command != nil || model.screen != screenResults {
		t.Fatalf("enter on Skip installed=%v screen=%v", called, model.screen)
	}
}

func TestInstallShowsAnimatedProgress(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	reported := make(chan struct{})
	model := NewModel([]provider.Result{{Filename: "Example.otf", Format: "otf"}}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts", Install: func(_ []string, _ bool, progress InstallProgressFunc) ([]string, error) {
		progress(1, 1, true)
		close(reported)
		<-release
		return []string{"/fonts/Example.otf"}, nil
	}})
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	model, command = press(model, "y")
	batch := command().(tea.BatchMsg)
	done := make(chan tea.Msg, 1)
	go func() { done <- batch[0]() }()
	<-reported
	before := model.View()
	if !strings.Contains(before, "Refreshing font cache") {
		t.Fatalf("install progress missing:\n%s", before)
	}
	updated, next := model.Update(downloadTickMessage{id: model.downloadID})
	model = updated.(Model)
	if next == nil || model.View() == before {
		t.Fatal("install progress did not animate on tick")
	}
	close(release)
	updated, _ = model.Update(<-done)
	model = updated.(Model)
	if !strings.Contains(model.View(), "Installed 1 file(s) to /fonts") {
		t.Fatalf("install result missing:\n%s", model.View())
	}
}
