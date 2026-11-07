package tui

import (
	"context"
	"errors"
	"fmt"
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
	var requested []string
	model := NewModel([]provider.Result{
		{Filename: "Alpha-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture", URL: "https://example.test/alpha-regular.otf"},
		{Filename: "Alpha-Bold.otf", Format: "otf", Weight: "bold", Source: "fixture", URL: "https://example.test/alpha-bold.otf"},
	}, func(result provider.Result, _ ProgressFunc) (string, error) {
		requested = append(requested, provider.ResultIdentity(result))
		// Fail the second file of each run so one file is saved first.
		if len(requested)%2 == 0 {
			return "", errors.New("fixture couldn't provide the font (HTTP 404)")
		}
		return "/tmp/" + result.Filename, nil
	}, false)
	model, _ = press(model, "enter")
	if model.screen != screenPreview {
		t.Fatalf("preview did not open: %v", model.screen)
	}
	reviewedGroup := model.currentGroup().GroupID
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	view := model.View()
	for _, wanted := range []string{"Download failed", "HTTP 404", "Alpha-", "Saved before the failure"} {
		if !strings.Contains(view, wanted) {
			t.Fatalf("error view lost %q:\n%s", wanted, view)
		}
	}
	firstRun := append([]string(nil), requested...)

	// Live results arrive while the error is shown: a new family member that
	// shifts file indices and other families that can reorder the groups.
	for _, live := range []provider.Result{
		{Filename: "Alpha-Light.otf", Format: "otf", Weight: "light", Source: "fixture", URL: "https://example.test/alpha-light.otf"},
		{Filename: "Aardvark-Regular.otf", Format: "otf", Weight: "regular", Source: "other", URL: "https://example.test/aardvark.otf"},
		{Filename: "Zeta-Regular.otf", Format: "otf", Weight: "regular", Source: "zeta", URL: "https://example.test/zeta.otf"},
	} {
		updated, _ := model.Update(eventMessage{event: provider.Event{Provider: "fixture", Type: provider.EventResult, Result: live}, open: true})
		model = updated.(Model)
	}

	model, command = press(model, "r")
	if command == nil || model.screen != screenDownloading {
		t.Fatalf("retry did not restart the download: screen=%v", model.screen)
	}
	model = runDownload(t, model, command)
	if strings.Join(requested[2:], ",") != strings.Join(firstRun, ",") {
		t.Fatalf("retry requested %v, want the original %v", requested[2:], firstRun)
	}
	model, _ = press(model, "esc")
	if model.screen != screenPreview || model.currentGroup().GroupID != reviewedGroup {
		t.Fatalf("dismissing the error left the reviewed preview: screen=%v group=%q want %q", model.screen, model.currentGroup().GroupID, reviewedGroup)
	}
}

func TestRetryFromResultsListIgnoresLiveReordering(t *testing.T) {
	t.Parallel()
	var requested []string
	model := NewModel([]provider.Result{
		{Filename: "Gamma-Regular.otf", Format: "otf", Weight: "regular", Source: "gamma", URL: "https://example.test/gamma.otf"},
		{Filename: "Beta-Regular.otf", Format: "otf", Weight: "regular", Source: "beta", URL: "https://example.test/beta.otf"},
	}, func(result provider.Result, _ ProgressFunc) (string, error) {
		requested = append(requested, provider.ResultIdentity(result))
		return "", errors.New("fixture couldn't provide the font (HTTP 503)")
	}, false)
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	if len(requested) != 1 {
		t.Fatalf("first run requested %v", requested)
	}
	// A live result that ranks first moves every group down one row, so the
	// numeric cursor now points at a different family.
	updated, _ := model.Update(eventMessage{event: provider.Event{Provider: "fixture", Type: provider.EventResult, Result: provider.Result{
		Filename: "Alpha-Regular.otf", Format: "otf", Weight: "regular", Source: "alpha", URL: "https://example.test/alpha.otf",
	}}, open: true})
	model = updated.(Model)
	if provider.ResultIdentity(model.currentGroup().Files[0]) == requested[0] {
		t.Fatal("fixture no longer moves the cursor; the test would not catch a retargeted retry")
	}
	model, command = press(model, "r")
	model = runDownload(t, model, command)
	if len(requested) != 2 || requested[1] != requested[0] {
		t.Fatalf("retry requested %v, want %q again", requested[1:], requested[0])
	}
}

func TestDownloadProgressEscapesControlCharacters(t *testing.T) {
	t.Parallel()
	model := NewModel(nil, nil, false)
	model.screen = screenDownloading
	model.progress = &downloadProgress{count: 1, total: -1}
	model.progress.start(0, 1, "Evil\x1b]0;pwned\x07.otf")
	view := strings.Join(model.downloadingLines(), "\n")
	if strings.ContainsAny(view, "\x1b\x07") || !strings.Contains(view, `Evil\u001B]0;pwned\u0007.otf`) {
		t.Fatalf("control characters reached the terminal: %q", view)
	}
}

func TestInstallSkipsUnsupportedFilesWithExplanation(t *testing.T) {
	t.Parallel()
	var installed []string
	model := NewModel([]provider.Result{
		{Filename: "Example-Regular.otf", Format: "otf", Weight: "regular", Source: "fixture", URL: "https://example.test/a.otf"},
		{Filename: "Example-Bold.woff2", Format: "woff2", Weight: "bold", Source: "fixture", URL: "https://example.test/b.woff2"},
	}, func(result provider.Result, _ ProgressFunc) (string, error) {
		return "/downloads/" + result.Filename, nil
	}, false).WithInstaller(&Installer{Dir: "/fonts",
		Supports: func(path string) bool { return !strings.HasSuffix(path, ".woff2") },
		Install: func(paths []string, _ bool, _ InstallProgressFunc) ([]string, error) {
			installed = append(installed, paths...)
			return []string{"/fonts/Example-Regular.otf"}, nil
		}})
	model, _ = press(model, "enter")
	model.selectAllCurrentGroup()
	model, command := press(model, "D")
	model = runDownload(t, model, command)
	view := model.View()
	if !strings.Contains(view, "Copy 1 file(s)") || !strings.Contains(view, "Convert WOFF/WOFF2") {
		t.Fatalf("install prompt did not explain the skipped web font:\n%s", view)
	}
	model, command = press(model, "y")
	model = runInstall(t, model, command)
	if strings.Join(installed, ",") != "/downloads/Example-Regular.otf" || !strings.Contains(model.View(), "Installed 1 file(s)") {
		t.Fatalf("installed=%v view:\n%s", installed, model.View())
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

func TestDownloadResultsEscapeUntrustedText(t *testing.T) {
	t.Parallel()
	// These standalone control/format characters are inert fixture text,
	// not terminal commands. Keep the original file identities unchanged.
	name := "Example\t\r\x1b\x07\u202e.otf"
	path := "/downloads/" + name
	model := NewModel(nil, nil, false).WithInstaller(&Installer{
		Dir:     "/fonts\tlocal",
		Install: func([]string, bool, InstallProgressFunc) ([]string, error) { return nil, nil },
	})
	assertEscaped := func(text string) {
		t.Helper()
		if strings.ContainsAny(text, "\t\r\x1b\x07\u202e") {
			t.Fatalf("unescaped control or format character: %q", text)
		}
	}

	model = model.finishDownload(downloadMessage{paths: []string{path}, err: errors.New(name + ": download failed")})
	assertEscaped(strings.Join(model.downloadResultLines(), "\n"))
	assertEscaped(model.status)
	if model.downloadPaths[0] != path {
		t.Fatal("escaping changed the filesystem path")
	}

	model = model.finishDownload(downloadMessage{paths: []string{path}})
	assertEscaped(strings.Join(model.downloadResultLines(), "\n"))
	assertEscaped(model.status)
	model = model.finishInstall(installMessage{err: fakeConflict{names: []string{name}}})
	assertEscaped(strings.Join(model.downloadResultLines(), "\n"))
	model, _ = press(model, "n")
	assertEscaped(model.status)

	model = model.finishInstall(installMessage{paths: []string{path}, err: errors.New(name + ": install failed")})
	assertEscaped(strings.Join(model.downloadResultLines(), "\n"))
	assertEscaped(model.status)
	model = model.finishInstall(installMessage{paths: []string{path}})
	assertEscaped(model.status)
	if model.installPaths[0] != path {
		t.Fatal("escaping changed the installed path")
	}
}

func TestDisplayPathEscapesAfterHomeAbbreviation(t *testing.T) {
	t.Setenv("HOME", "/home/example")
	for _, path := range []string{"/home/example/Fonts\tname", "/elsewhere/Fonts\tname"} {
		displayed := displayPath(path)
		if strings.ContainsRune(displayed, '\t') || !strings.Contains(displayed, `\u0009`) {
			t.Fatalf("displayPath(%q) = %q", path, displayed)
		}
	}
}

func TestInstallPromptIsVisibleBeforeConfirmation(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"enter", "y"} {
		t.Run(key, func(t *testing.T) {
			model := NewModel(nil, nil, false).WithInstaller(&Installer{
				Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) { return nil, nil },
			})
			model.width, model.height = 80, 16
			var paths []string
			for index := range 40 {
				paths = append(paths, fmt.Sprintf("/downloads/font-%02d.otf", index))
			}
			model = model.finishDownload(downloadMessage{paths: paths})
			view := model.View()
			if !model.installPromptVisible() || !strings.Contains(view, "Copy 40 file(s) to /fonts") || !strings.Contains(view, "[ Install ]") {
				t.Fatalf("long summary hid initial consent:\n%s", view)
			}
			assertViewFits(t, view, 80, 16)
			for model.detailOffset > 0 {
				model, _ = press(model, "k")
			}
			if !strings.Contains(model.View(), "font-00.otf") || model.installPromptVisible() {
				t.Fatalf("summary could not be inspected:\n%s", model.View())
			}
			model, command := press(model, key)
			if command != nil || model.installState != installPrompt || !model.installPromptVisible() {
				t.Fatalf("hidden prompt accepted %q: state=%v command=%v", key, model.installState, command != nil)
			}
			model, command = press(model, key)
			if command == nil || model.installState != installRunning {
				t.Fatal("visible confirmation did not start installation")
			}
		})
	}
}

func TestInstallPromptShortTerminalAndResize(t *testing.T) {
	t.Parallel()
	model := NewModel(nil, nil, false).WithInstaller(&Installer{
		Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) { return nil, nil },
	})
	model.width, model.height = 40, 10
	model = model.finishDownload(downloadMessage{paths: []string{"/downloads/Example.otf"}})
	view := model.View()
	if !model.installPromptVisible() || !strings.Contains(view, "/fonts") || !strings.Contains(view, "[ Install ]") {
		t.Fatalf("compact prompt hidden:\n%s", view)
	}
	assertViewFits(t, view, 40, 10)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	model = updated.(Model)
	for _, key := range []string{"enter", "y", "y"} {
		var command tea.Cmd
		model, command = press(model, key)
		if command != nil || model.installState != installPrompt {
			t.Fatalf("undersized terminal accepted %q", key)
		}
	}
	assertViewFits(t, model.View(), 20, 5)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	model, command := press(model, "enter")
	if command == nil || model.installState != installRunning {
		t.Fatal("enlarged visible prompt could not be confirmed")
	}
}

func TestOverwriteRequiresVisibleQuestionAndSelection(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"enter", "y", "Y"} {
		t.Run(key, func(t *testing.T) {
			model := NewModel(nil, nil, false).WithInstaller(&Installer{
				Dir: "/fonts", Install: func([]string, bool, InstallProgressFunc) ([]string, error) { return nil, nil },
			})
			model.width, model.height = 80, 16
			var paths []string
			for index := range 30 {
				paths = append(paths, fmt.Sprintf("/downloads/font-%02d.otf", index))
			}
			model = model.finishDownload(downloadMessage{paths: paths})
			model = model.finishInstall(installMessage{err: fakeConflict{names: []string{"font-00.otf"}}})
			if !model.installPromptVisible() || !strings.Contains(model.View(), "[ Keep existing ]") {
				t.Fatalf("overwrite question/default hidden:\n%s", model.View())
			}
			model, _ = press(model, "right")
			for model.detailOffset > 0 {
				model, _ = press(model, "k")
			}
			model, command := press(model, key)
			if command != nil || model.installState != installConflict || !model.installPromptVisible() || !strings.Contains(model.View(), "[ Overwrite ]") || !strings.Contains(model.View(), "/fonts") {
				t.Fatalf("hidden overwrite accepted or question not shown:\n%s", model.View())
			}
			model, command = press(model, key)
			if command == nil || model.installState != installRunning {
				t.Fatal("visible overwrite could not be confirmed")
			}
		})
	}
}

func TestInstallerShutdownCancelsAndWaitsForCleanup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release, cleaned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	original := &Installer{Dir: "/fonts", Cancel: cancel, Install: func([]string, bool, InstallProgressFunc) ([]string, error) {
		close(started)
		defer close(cleaned)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}}
	model, shutdown := NewModel(nil, nil, false).WithInstaller(original).manageInstaller()
	result := make(chan error, 1)
	go func() {
		_, err := model.installer.Install(nil, false, nil)
		result <- err
	}()
	<-started
	finished := make(chan struct{})
	go func() {
		shutdown()
		close(finished)
	}()
	<-ctx.Done()
	select {
	case <-finished:
		t.Fatal("shutdown returned before installer cleanup")
	default:
	}
	close(release)
	<-finished
	select {
	case <-cleaned:
	default:
		t.Fatal("cleanup not finished")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("install result: %v", err)
	}
	if _, err := model.installer.Install(nil, false, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("late queued command was not rejected: %v", err)
	}
	if model.installer == original {
		t.Fatal("shutdown management mutated shared installer")
	}
}
