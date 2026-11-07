package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/microck/moji/internal/provider"
)

// ProgressFunc receives bytes transferred so far and the expected total, which
// is -1 when unknown.
type ProgressFunc func(written, total int64)

// Installer copies downloaded files into a user font directory. A nil
// Installer, or one with an empty Dir, disables the install prompt.
type Installer struct {
	Dir string
	// Install copies paths into Dir. Unless overwrite is true it must refuse to
	// replace a different file and return an InstallConflict error instead. It
	// reports each step through progress, which is never nil.
	Install func(paths []string, overwrite bool, progress InstallProgressFunc) (installed []string, err error)
	// Supports reports whether path can be installed by copying it into Dir.
	// Unsupported files are left out of the install and explained instead. A
	// nil Supports accepts every file.
	Supports func(path string) bool
	// Cancel requests shutdown of an active Install. It must be safe to call
	// concurrently with Install. Run waits for Install to return after Cancel.
	Cancel context.CancelFunc
}

// InstallProgressFunc reports that done of total files are copied. refreshing
// is true while the font cache is being rebuilt after the copy.
type InstallProgressFunc func(done, total int, refreshing bool)

// InstallConflict is implemented by Install errors that mean "a different
// font with this name is already installed", so the TUI can offer to
// overwrite it instead of failing.
type InstallConflict interface {
	error
	ConflictNames() []string
}

// WithInstaller enables the prompt that offers to copy finished downloads into
// installer.Dir.
func (model Model) WithInstaller(installer *Installer) Model {
	model.installer = installer
	return model
}

type installState int

const (
	installNone installState = iota
	installPrompt
	installRunning
	installConflict
	installDone
)

// downloadProgress is shared between the download goroutine and the model
// copies Bubble Tea keeps, so it lives behind a pointer and a mutex.
type downloadProgress struct {
	mu       sync.Mutex
	index    int
	count    int
	filename string
	written  int64
	total    int64
}

func (progress *downloadProgress) start(index, count int, filename string) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	progress.index, progress.count, progress.filename = index, count, filename
	progress.written, progress.total = 0, -1
}

func (progress *downloadProgress) update(written, total int64) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	progress.written, progress.total = written, total
}

func (progress *downloadProgress) snapshot() (index, count int, filename string, written, total int64) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	return progress.index, progress.count, progress.filename, progress.written, progress.total
}

type downloadTickMessage struct{ id int }

type installMessage struct {
	paths []string
	err   error
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const downloadTickInterval = 120 * time.Millisecond

func downloadTick(id int) tea.Cmd {
	return tea.Tick(downloadTickInterval, func(time.Time) tea.Msg { return downloadTickMessage{id: id} })
}

// startDownload switches to the progress screen and returns the commands that
// run the download and animate the progress view.
func (model Model) startDownload() (Model, tea.Cmd) {
	group := model.currentGroup()
	selected := make([]provider.Result, 0, len(group.Files))
	for index, result := range group.Files {
		if model.selectedFiles[index] {
			selected = append(selected, result)
		}
	}
	if len(selected) == 0 || model.downloader == nil {
		return model, nil
	}
	if model.screen != screenConfirm {
		model.returnScreen = model.screen
	}
	return model.runDownload(selected)
}

// runDownload downloads exactly selected. It keeps the list so a retry fetches
// the same fonts even if live results reorder the groups in the meantime.
func (model Model) runDownload(selected []provider.Result) (Model, tea.Cmd) {
	model.retryResults = selected
	model.downloadID++
	model.screen = screenDownloading
	model.spinnerFrame = 0
	model.downloadPaths = nil
	model.downloadErr = nil
	model.installState = installNone
	model.installPaths = nil
	model.installErr = nil
	model.status = ""
	progress := &downloadProgress{count: len(selected), total: -1}
	model.progress = progress
	downloader := model.downloader
	run := func() tea.Msg {
		paths := make([]string, 0, len(selected))
		for index, result := range selected {
			progress.start(index, len(selected), result.Filename)
			path, err := downloader(result, progress.update)
			if err != nil {
				return downloadMessage{paths: paths, err: fmt.Errorf("%s: %w", result.Filename, err)}
			}
			paths = append(paths, path)
		}
		return downloadMessage{paths: paths}
	}
	return model, tea.Batch(run, downloadTick(model.downloadID))
}

func (model Model) finishDownload(message downloadMessage) Model {
	model.progress = nil
	model.downloadPaths = message.paths
	model.downloadErr = message.err
	model.detailOffset = 0
	model.screen = screenDownloadResult
	if message.err != nil {
		model.status = "Download failed: " + escapeTerminalControls(message.err.Error())
		return model
	}
	model.status = fmt.Sprintf("Downloaded %d file(s): %s", len(message.paths), escapeTerminalControls(strings.Join(message.paths, ", ")))
	if model.installer != nil && model.installer.Dir != "" && model.installer.Install != nil && len(model.pathsToInstall()) > 0 {
		model.installState = installPrompt
		model.promptChoice = 0
		model = model.focusInstallPrompt()
	}
	return model
}

// pathsToInstall skips files that already live in the font directory, which
// happens when the download directory is the font directory.
func (model Model) pathsToInstall() []string {
	if model.installer == nil {
		return nil
	}
	paths, _ := model.splitInstallPaths()
	return paths
}

// unsupportedInstallPaths lists downloads the platform can't install as-is,
// such as WOFF/WOFF2 on macOS.
func (model Model) unsupportedInstallPaths() []string {
	_, unsupported := model.splitInstallPaths()
	return unsupported
}

func (model Model) splitInstallPaths() (paths, unsupported []string) {
	if model.installer == nil {
		return nil, nil
	}
	target := filepath.Clean(model.installer.Dir)
	paths = make([]string, 0, len(model.downloadPaths))
	for _, path := range model.downloadPaths {
		if path == "" || filepath.Clean(filepath.Dir(path)) == target {
			continue
		}
		if model.installer.Supports != nil && !model.installer.Supports(path) {
			unsupported = append(unsupported, path)
			continue
		}
		paths = append(paths, path)
	}
	return paths, unsupported
}

// unsupportedInstallNote explains why some downloads were left out of the
// install, or returns "" when every download can be installed.
func (model Model) unsupportedInstallNote() string {
	unsupported := model.unsupportedInstallPaths()
	if len(unsupported) == 0 {
		return ""
	}
	names := make([]string, len(unsupported))
	for index, path := range unsupported {
		names[index] = escapeTerminalControls(filepath.Base(path))
	}
	return fmt.Sprintf("Not installed: %s. Convert WOFF/WOFF2 files to TTF or OTF before installing them on this system.", strings.Join(names, ", "))
}

// installRefreshLabel marks the font-cache step in the shared progress state.
const installRefreshLabel = "\x00refresh"

func (model Model) startInstall(overwrite bool) (Model, tea.Cmd) {
	install := model.installer.Install
	paths := model.pathsToInstall()
	model.installState = installRunning
	model.downloadID++
	model.spinnerFrame = 0
	progress := &downloadProgress{count: len(paths), total: -1}
	model.progress = progress
	report := func(done, total int, refreshing bool) {
		label := ""
		if refreshing {
			label = installRefreshLabel
		}
		progress.start(done, total, label)
	}
	run := func() tea.Msg {
		installed, err := install(paths, overwrite, report)
		return installMessage{paths: installed, err: err}
	}
	return model, tea.Batch(run, downloadTick(model.downloadID))
}

// animating reports whether a background download or install is running, so
// the spinner tick should keep firing.
func (model Model) animating() bool {
	return model.screen == screenDownloading || model.screen == screenDownloadResult && model.installState == installRunning
}

// promptButtons returns the labels for the active install question. Index 0
// is always the action that writes to the font directory.
func (model Model) promptButtons() []string {
	switch model.installState {
	case installPrompt:
		return []string{"Install", "Skip"}
	case installConflict:
		return []string{"Overwrite", "Keep existing"}
	}
	return nil
}

func (model Model) updateDownloadResult(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	switch key {
	case "ctrl+c":
		return model, tea.Quit
	case "up", "k":
		model.detailOffset = max(0, model.detailOffset-1)
		return model, nil
	case "down", "j":
		maximum := max(0, len(model.downloadResultLines())-model.bodyHeight())
		model.detailOffset = min(maximum, model.detailOffset+1)
		return model, nil
	}
	switch model.installState {
	case installRunning:
		return model, nil
	case installPrompt, installConflict:
		accept := false
		switch key {
		case "left", "right", "h", "l", "tab", "shift+tab":
			model.promptChoice = 1 - model.promptChoice
			return model, nil
		case "enter":
			accept = model.promptChoice == 0
		case "y", "Y":
			accept = true
		case "n", "N", "esc", "q":
		default:
			return model, nil
		}
		if accept {
			// Scrolling or resizing must never leave a hidden affirmative action
			// active. First show the complete question and selection; a later key
			// can confirm it. An undersized terminal must be enlarged first.
			if !model.installPromptVisible() {
				return model.focusInstallPrompt(), nil
			}
			return model.startInstall(model.installState == installConflict)
		}
		if model.installState == installConflict {
			model.status = "Install skipped: " + escapeTerminalControls(strings.Join(model.conflictNames, ", ")) + " already installed"
		} else {
			model.status += "  (not installed)"
		}
		model.installState = installNone
		return model.leaveDownloadResult(), nil
	}
	switch key {
	case "enter", "esc", "q", " ":
		return model.leaveDownloadResult(), nil
	case "D", "r":
		if model.downloadErr != nil && len(model.retryResults) > 0 {
			// Retry the fonts that were reviewed, not whatever the cursor now
			// points at after live results reordered the list.
			return model.runDownload(model.retryResults)
		}
	}
	return model, nil
}

func (model Model) leaveDownloadResult() Model {
	model.screen = model.returnScreen
	if model.screen == screenConfirm || model.screen == screenDownloading || model.screen == screenDownloadResult {
		model.screen = screenResults
	}
	model.detailOffset = 0
	return model
}

func (model Model) finishInstall(message installMessage) Model {
	model.progress = nil
	var conflict InstallConflict
	if errors.As(message.err, &conflict) {
		model.installState = installConflict
		// Overwriting is destructive, so the safe answer is the default.
		model.promptChoice = 1
		model.conflictNames = conflict.ConflictNames()
		return model.focusInstallPrompt()
	}
	model.installState = installDone
	model.installPaths = message.paths
	model.installErr = message.err
	model.detailOffset = 0
	if message.err != nil {
		model.status = "Install failed: " + escapeTerminalControls(message.err.Error())
	} else {
		model.status = fmt.Sprintf("Installed %d file(s) to %s", len(message.paths), displayPath(model.installer.Dir))
	}
	return model
}

func (model Model) viewDownloading() string {
	context := "downloading"
	return model.chrome(context, strings.Join(model.downloadingLines(), "\n"), model.faint.Render("please wait  ctrl+c: quit"))
}

func (model Model) downloadingLines() []string {
	width := model.contentWidth()
	spinner := spinnerFrames[model.spinnerFrame%len(spinnerFrames)]
	if model.progress == nil {
		return []string{"", "  " + model.warning.Render(spinner+" Starting download...")}
	}
	index, count, filename, written, total := model.progress.snapshot()
	heading := fmt.Sprintf("%s Downloading file %d of %d", spinner, index+1, count)
	lines := []string{"", "  " + model.warning.Render(heading), ""}
	for _, line := range wrapCells(escapeTerminalControls(filename), max(1, width-2)) {
		lines = append(lines, "  "+model.accent.Render(line))
	}
	lines = append(lines, "")
	barWidth := max(4, min(48, width-22))
	if total > 0 {
		fraction := min(1, float64(written)/float64(total))
		filled := int(fraction * float64(barWidth))
		bar := model.success.Render(strings.Repeat("█", filled)) + model.faint.Render(strings.Repeat("░", barWidth-filled))
		lines = append(lines, truncate(fmt.Sprintf("  %s %3d%%", bar, int(fraction*100)), width))
		lines = append(lines, model.secondary.Render(fmt.Sprintf("  %s of %s", formatBytes(written), formatBytes(total))))
	} else {
		// Without a length the best honest signal is a moving marker plus a
		// byte counter, so the screen still visibly advances.
		position := model.spinnerFrame % barWidth
		bar := model.faint.Render(strings.Repeat("░", position)) + model.success.Render("█") + model.faint.Render(strings.Repeat("░", barWidth-position-1))
		lines = append(lines, "  "+bar)
		lines = append(lines, model.secondary.Render("  "+formatBytes(written)+" received"))
	}
	if count > 1 {
		lines = append(lines, "", model.secondary.Render(fmt.Sprintf("  %d of %d files finished", index, count)))
	}
	return lines
}

func (model Model) viewDownloadResult() string {
	lines := model.downloadResultLines()
	maximum := max(0, len(lines)-model.bodyHeight())
	offset := min(model.detailOffset, maximum)
	end := min(len(lines), offset+model.bodyHeight())
	context := "download complete"
	if model.installState == installPrompt {
		context = "download complete - install?"
	} else if model.installState == installConflict {
		context = "font already installed"
	} else if model.installState == installRunning {
		context = "installing"
	}
	if model.downloadErr != nil {
		context = "download failed"
	} else if model.installState == installDone && model.installErr != nil {
		context = "install failed"
	} else if model.installState == installDone {
		context = "installed"
	}
	return model.chrome(context, strings.Join(lines[offset:end], "\n"), model.downloadResultHelp())
}

func (model Model) downloadResultLines() []string {
	width := max(1, model.contentWidth()-4)
	lines := []string{""}
	addWrapped := func(text string, render func(...string) string) {
		// Errors and local paths can contain provider-supplied filenames too.
		// Escape before measuring, wrapping, or applying trusted styling.
		for _, line := range wrapCells(escapeTerminalControls(text), width) {
			lines = append(lines, "  "+render(line))
		}
	}
	plain := func(values ...string) string { return strings.Join(values, "") }
	if model.downloadErr != nil {
		addWrapped("✗ Download failed", model.danger.Render)
		lines = append(lines, "")
		addWrapped(model.downloadErr.Error(), plain)
	} else {
		addWrapped(fmt.Sprintf("✓ Downloaded %d file(s)", len(model.downloadPaths)), model.success.Render)
	}
	if len(model.downloadPaths) > 0 {
		lines = append(lines, "")
		if model.downloadErr != nil {
			addWrapped("Saved before the failure:", model.secondary.Render)
		}
		for _, path := range model.downloadPaths {
			addWrapped("  "+displayPath(path), plain)
		}
	}
	if note := model.unsupportedInstallNote(); note != "" && model.downloadErr == nil {
		lines = append(lines, "")
		addWrapped(note, model.secondary.Render)
	}
	switch model.installState {
	case installPrompt, installConflict:
		lines = append(lines, "")
		lines = append(lines, model.installPromptLines()...)
	case installRunning:
		lines = append(lines, "")
		lines = append(lines, model.installingLines()...)
	case installDone:
		lines = append(lines, "")
		if model.installErr != nil {
			addWrapped("✗ Install failed", model.danger.Render)
			addWrapped(model.installErr.Error(), plain)
		} else {
			addWrapped(fmt.Sprintf("✓ Installed %d file(s) to %s", len(model.installPaths), displayPath(model.installer.Dir)), model.success.Render)
		}
		if len(model.installPaths) > 0 && model.installErr != nil {
			addWrapped("Installed before the failure:", model.secondary.Render)
			for _, path := range model.installPaths {
				addWrapped("  "+displayPath(path), plain)
			}
		}
	}
	return lines
}

func (model Model) installPromptLines() []string {
	switch model.installState {
	case installPrompt:
		return model.promptCard("Install this font?",
			fmt.Sprintf("Copy %d file(s) to %s to install?", len(model.pathsToInstall()), displayPath(model.installer.Dir)))
	case installConflict:
		noun := "a different font named"
		if len(model.conflictNames) > 1 {
			noun = "different fonts named"
		}
		return model.promptCard("Font already installed",
			fmt.Sprintf("%s already has %s %s. Overwrite?", displayPath(model.installer.Dir), noun, strings.Join(model.conflictNames, ", ")))
	}
	return nil
}

func (model Model) focusInstallPrompt() Model {
	model.detailOffset = max(0, len(model.downloadResultLines())-model.bodyHeight())
	return model
}

func (model Model) installPromptVisible() bool {
	prompt := model.installPromptLines()
	if len(prompt) == 0 || len(prompt) > model.bodyHeight() {
		return false
	}
	for _, line := range prompt {
		if lipgloss.Width(line) > model.contentWidth() {
			return false
		}
	}
	count := len(model.downloadResultLines())
	offset := min(model.detailOffset, max(0, count-model.bodyHeight()))
	// The prompt is the final block. Both its first line and its final button
	// line must be inside the viewport, not merely the current selection.
	return offset <= count-len(prompt) && offset+model.bodyHeight() >= count
}

func (model Model) downloadResultHelp() string {
	if model.installState == installPrompt || model.installState == installConflict {
		if !model.installPromptVisible() {
			if len(model.installPromptLines()) > model.bodyHeight() {
				return model.faint.Render("resize to confirm  n/esc: skip")
			}
			return model.faint.Render("enter/y: show prompt  j/k: scroll  n/esc: skip")
		}
		if model.contentWidth() < 34 {
			return model.faint.Render("<-/-> enter  y/n")
		}
		buttons := model.promptButtons()
		return model.faint.Render(fmt.Sprintf("<-/->: choose  enter: confirm  y: %s  n/esc: %s", strings.ToLower(buttons[0]), strings.ToLower(buttons[1])))
	}
	if model.installState == installRunning {
		return model.faint.Render("please wait  ctrl+c: quit")
	}
	if model.downloadErr != nil {
		if model.contentWidth() < 34 {
			return model.faint.Render("r retry  esc back")
		}
		return model.faint.Render("r: retry  j/k: scroll  enter/esc: back")
	}
	return model.faint.Render("enter/esc: back")
}

// promptCard draws the install question as a bordered card with buttons so it
// stands out from the download summary above it.
func (model Model) promptCard(title, question string) []string {
	boxWidth := max(12, min(64, model.contentWidth()-4))
	inner := max(1, boxWidth-4)
	body := []string{model.warning.Render(truncate(title, inner)), ""}
	for _, line := range wrapCells(escapeTerminalControls(question), inner) {
		body = append(body, line)
	}
	buttons := model.promptButtons()
	rendered := make([]string, len(buttons))
	for index, label := range buttons {
		if index == model.promptChoice {
			rendered[index] = model.button.Render("[ " + label + " ]")
		} else {
			rendered[index] = model.faint.Render("  " + label + "  ")
		}
	}
	body = append(body, "", strings.Join(rendered, "   "))
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(model.warning.GetForeground()).
		Padding(0, 1).
		Width(boxWidth - 2).
		Render(strings.Join(body, "\n"))
	lines := strings.Split(card, "\n")
	for index := range lines {
		lines[index] = "  " + lines[index]
	}
	fits := len(lines) <= model.bodyHeight()
	for _, line := range lines {
		fits = fits && lipgloss.Width(line) <= model.contentWidth()
	}
	if !fits {
		// Drop decoration before dropping any consent text. If even this
		// compact question cannot fit, confirmation remains disabled.
		width := max(1, model.contentWidth()-2)
		lines = wrapCells(escapeTerminalControls(question), width)
		for index, label := range buttons {
			if index == model.promptChoice {
				label = "[ " + label + " ]"
			}
			lines = append(lines, wrapCells(label, width)...)
		}
		for index := range lines {
			lines[index] = "  " + lines[index]
		}
	}
	return lines
}

func (model Model) installingLines() []string {
	spinner := spinnerFrames[model.spinnerFrame%len(spinnerFrames)]
	barWidth := max(4, min(48, model.contentWidth()-22))
	done, count, label := 0, 0, ""
	if model.progress != nil {
		done, count, label, _, _ = model.progress.snapshot()
	}
	var heading, bar string
	if label == installRefreshLabel {
		heading = spinner + " Refreshing font cache..."
		position := model.spinnerFrame % barWidth
		bar = model.faint.Render(strings.Repeat("░", position)) + model.success.Render("█") + model.faint.Render(strings.Repeat("░", barWidth-position-1))
	} else {
		heading = fmt.Sprintf("%s Installing file %d of %d to %s", spinner, min(count, done+1), max(1, count), displayPath(model.installer.Dir))
		filled := 0
		if count > 0 {
			filled = done * barWidth / count
		}
		bar = model.success.Render(strings.Repeat("█", filled)) + model.faint.Render(strings.Repeat("░", barWidth-filled))
	}
	return []string{"  " + model.warning.Render(truncate(heading, max(1, model.contentWidth()-2))), "", "  " + bar}
}

// escapeTerminalControls makes control and format characters visible as
// \uXXXX so provider-supplied text cannot drive the terminal.
func escapeTerminalControls(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			fmt.Fprintf(&escaped, "\\u%04X", character)
			continue
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

func formatBytes(value int64) string {
	switch {
	case value >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(value)/(1<<20))
	case value >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(value)/(1<<10))
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return escapeTerminalControls(path)
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return escapeTerminalControls("~" + path[len(home):])
	}
	return escapeTerminalControls(path)
}

// manageInstaller joins commands before Run returns, including when Bubble Tea
// exits through a signal or an I/O error. The lock also rejects commands that
// were queued but had not started when shutdown began.
func (model Model) manageInstaller() (Model, func() error) {
	if model.installer == nil || model.installer.Install == nil {
		return model, func() error { return nil }
	}
	installer := *model.installer
	install := installer.Install
	var mutex sync.Mutex
	var active sync.WaitGroup
	stopped := false
	var installErr error
	installer.Install = func(paths []string, overwrite bool, progress InstallProgressFunc) ([]string, error) {
		mutex.Lock()
		if stopped {
			mutex.Unlock()
			return nil, context.Canceled
		}
		active.Add(1)
		installErr = nil
		mutex.Unlock()
		defer active.Done()
		paths, err := install(paths, overwrite, progress)
		mutex.Lock()
		installErr = err
		mutex.Unlock()
		return paths, err
	}
	model.installer = &installer
	return model, func() error {
		mutex.Lock()
		stopped = true
		mutex.Unlock()
		if installer.Cancel != nil {
			installer.Cancel()
		}
		active.Wait()
		mutex.Lock()
		defer mutex.Unlock()
		if cancellationOnly(installErr) {
			return nil
		}
		return installErr
	}
}

// Keep cleanup failures joined with cancellation, while treating an ordinary
// Ctrl+C (including a wrapped cancellation) as a clean interactive exit.
func cancellationOnly(err error) bool {
	if err == nil || err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !cancellationOnly(child) {
				return false
			}
		}
		return len(joined.Unwrap()) > 0
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return wrapped.Unwrap() != nil && cancellationOnly(wrapped.Unwrap())
	}
	return false
}
