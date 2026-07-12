package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
)

type DownloadFunc func(provider.Result) (string, error)

type screen int

const (
	screenHome screen = iota
	screenResults
	screenPreview
	screenHealth
	screenConfirm
)

type Model struct {
	screen         screen
	homeHint       string
	query          string
	search         SearchFunc
	all            []provider.Result
	visible        []provider.Result
	groups         []rank.ResultGroup
	resultsWindow  listWindow
	previewWindow  listWindow
	selectedFiles  map[int]bool
	filter         string
	filtering      bool
	format         string
	sortMode       int
	detailOffset   int
	status         string
	providerStatus map[string]string
	events         <-chan provider.Event
	loading        bool
	returnScreen   screen
	wantedWeight   string
	ranking        rank.Weights
	maximum        int
	downloader     DownloadFunc
	brand          lipgloss.Style
	accent         lipgloss.Style
	faint          lipgloss.Style
	warning        lipgloss.Style
	success        lipgloss.Style
	danger         lipgloss.Style
	providerStyles map[string]lipgloss.Style
	width          int
	height         int
}

type downloadMessage struct {
	paths []string
	err   error
}

type eventMessage struct {
	event provider.Event
	open  bool
}

func NewModel(results []provider.Result, downloader DownloadFunc, color bool) Model {
	model := Model{
		all: append([]provider.Result(nil), results...), downloader: downloader,
		format: "all", providerStatus: make(map[string]string), ranking: rank.DefaultWeights(), screen: screenResults,
		selectedFiles: make(map[int]bool),
	}
	if color {
		model.brand = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8C00")).Bold(true)
		model.accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
		model.faint = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
		model.warning = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F")).Bold(true)
		model.success = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAF5F"))
		model.danger = lipgloss.NewStyle().Foreground(lipgloss.Color("#D75F5F"))
		model.providerStyles = map[string]lipgloss.Style{
			"github":    lipgloss.NewStyle().Foreground(lipgloss.Color("#AF87D7")),
			"getfonts":  lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAFD7")),
			"registry":  lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAF87")),
			"websearch": lipgloss.NewStyle().Foreground(lipgloss.Color("#D7AF5F")),
			"plugins":   lipgloss.NewStyle().Foreground(lipgloss.Color("#D787AF")),
		}
	}
	model.refresh()
	return model
}

func NewLiveModel(events <-chan provider.Event, downloader DownloadFunc, color bool, query, wantedWeight string, ranking rank.Weights, maximum int) Model {
	model := NewModel(nil, downloader, color)
	model.events = events
	model.loading = true
	model.query = rank.NormalizeQuery(query)
	model.wantedWeight = wantedWeight
	model.ranking = ranking
	model.maximum = maximum
	return model
}

func (model Model) Init() tea.Cmd { return model.waitForEvent() }

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := message.(tea.WindowSizeMsg); ok {
		// Some PTY implementations initially report 0x0. Keep the documented
		// fallback size until the terminal provides usable dimensions.
		if size.Width > 0 {
			model.width = size.Width
		}
		if size.Height > 0 {
			model.height = size.Height
		}
		return model, nil
	}
	if model.screen == screenHome {
		return model.updateHome(message)
	}
	switch message := message.(type) {
	case eventMessage:
		if !message.open {
			model.loading = false
			model.events = nil
			return model, nil
		}
		event := message.event
		if event.Type == provider.EventResult {
			model.all = provider.UniqueResults(append(model.all, event.Result))
			model.refresh()
		} else {
			model.providerStatus[event.Provider] = eventStatus(event)
		}
		return model, model.waitForEvent()
	case downloadMessage:
		if message.err != nil {
			model.status = "Download failed: " + message.err.Error()
		} else {
			model.status = fmt.Sprintf("Downloaded %d file(s): %s", len(message.paths), strings.Join(message.paths, ", "))
		}
		if model.screen == screenConfirm {
			model.screen = model.returnScreen
		}
		return model, nil
	case tea.KeyMsg:
		if model.filtering {
			switch message.String() {
			case "esc":
				model.filtering = false
			case "enter":
				model.filtering = false
				model.refresh()
			case "backspace":
				if len(model.filter) > 0 {
					model.filter = model.filter[:len(model.filter)-1]
					model.refresh()
				}
			default:
				if len(message.Runes) > 0 {
					model.filter += string(message.Runes)
					model.refresh()
				}
			}
			return model, nil
		}
		if model.screen == screenConfirm {
			switch message.String() {
			case "y", "Y", "enter":
				return model, model.downloadSelected()
			case "n", "N", "esc":
				model.screen = model.returnScreen
			}
			return model, nil
		}
		if model.screen == screenHealth {
			switch message.String() {
			case "ctrl+c", "q":
				return model, tea.Quit
			case "tab", "esc", "H":
				model.screen = screenResults
			case "r":
				if model.search == nil || strings.TrimSpace(model.query) == "" {
					model.status = "Re-check is available after a search started from the home screen."
					return model, nil
				}
				events, err := model.search(model.query)
				if err != nil {
					model.status = "Provider re-check couldn't start: " + err.Error()
					return model, nil
				}
				model.events = events
				model.loading = true
				model.providerStatus = make(map[string]string)
				return model, model.waitForEvent()
			}
			return model, nil
		}
		if model.screen == screenPreview && model.previewIsFamily() {
			switch message.String() {
			case "up", "k":
				model.previewWindow.move(-1, len(model.currentGroup().Files), model.bodyHeight()-2)
				return model, nil
			case "down", "j":
				model.previewWindow.move(1, len(model.currentGroup().Files), model.bodyHeight()-2)
				return model, nil
			case " ":
				index := model.previewWindow.cursor
				model.selectedFiles[index] = !model.selectedFiles[index]
				return model, nil
			case "a":
				for index := range model.currentGroup().Files {
					model.selectedFiles[index] = true
				}
				return model, nil
			case "n":
				clear(model.selectedFiles)
				return model, nil
			}
		}
		if model.screen == screenPreview {
			switch message.String() {
			case "up", "k":
				model.detailOffset = max(0, model.detailOffset-1)
				return model, nil
			case "down", "j":
				maximum := max(0, len(model.detailLines(model.currentGroup().Files[0]))-model.bodyHeight())
				model.detailOffset = min(maximum, model.detailOffset+1)
				return model, nil
			}
			switch message.String() {
			case "D", "esc", "q", "ctrl+c", "H":
				// These keys are handled by the shared screen actions below.
			default:
				return model, nil
			}
		}
		switch message.String() {
		case "ctrl+c", "q":
			return model, tea.Quit
		case "esc":
			if model.screen == screenPreview || model.screen == screenHealth {
				model.screen = screenResults
				model.detailOffset = 0
				return model, nil
			}
			if model.search != nil {
				model.screen = screenHome
				return model, nil
			}
			return model, tea.Quit
		case "up", "k":
			if model.screen != screenResults {
				break
			}
			model.resultsWindow.move(-1, len(model.groups), model.resultPageSize())
		case "down", "j":
			if model.screen != screenResults {
				break
			}
			model.resultsWindow.move(1, len(model.groups), model.resultPageSize())
		case "pgup":
			model.resultsWindow.move(-model.resultPageSize(), len(model.groups), model.resultPageSize())
		case "pgdown":
			model.resultsWindow.move(model.resultPageSize(), len(model.groups), model.resultPageSize())
		case "g", "home":
			model.resultsWindow.home()
		case "G", "end":
			model.resultsWindow.end(len(model.groups), model.resultPageSize())
		case "enter":
			if len(model.groups) > 0 {
				model.screen = screenPreview
				model.detailOffset = 0
				model.previewWindow.home()
				model.selectAllCurrentGroup()
			}
		case "H":
			model.screen = screenHealth
		case "tab":
			model.screen = screenHealth
		case "/":
			model.filtering = true
		case "f":
			formats := []string{"all", "otf", "ttf", "woff2"}
			for index, format := range formats {
				if model.format == format {
					model.format = formats[(index+1)%len(formats)]
					break
				}
			}
			model.refresh()
		case "o":
			model.sortMode = (model.sortMode + 1) % 3
			model.refresh()
		case "D":
			if len(model.groups) == 0 || model.downloader == nil {
				return model, nil
			}
			if model.screen != screenPreview {
				model.selectedFiles = map[int]bool{0: true}
			}
			if model.selectedCount() > 3 {
				model.returnScreen = model.screen
				model.screen = screenConfirm
				return model, nil
			}
			return model, model.downloadSelected()
		}
	}
	return model, nil
}

func (model Model) View() string {
	if model.screen == screenHome {
		return model.viewHome()
	}
	if model.screen == screenHealth {
		context := "provider health"
		if model.contentWidth() < 34 {
			context = "health"
		}
		return model.chrome(context, model.healthBody(), model.healthHelp())
	}
	if model.screen == screenConfirm {
		body := fmt.Sprintf("\n  Download %d selected files?\n\n  Existing identical files will be reused.", model.selectedCount())
		context := "confirm download"
		if model.contentWidth() < 34 {
			context = "confirm"
		}
		return model.chrome(context, body, model.confirmHelp())
	}
	if model.screen == screenPreview && len(model.groups) > 0 {
		if model.previewIsFamily() {
			return model.chrome(model.previewContext(), model.familyPreviewBody(), model.familyPreviewHelp())
		}
		result := model.currentGroup().Files[0]
		lines := model.detailLines(result)
		maximum := max(0, len(lines)-model.bodyHeight())
		offset := min(model.detailOffset, maximum)
		end := min(len(lines), offset+model.bodyHeight())
		context := "font details"
		if len(lines) > model.bodyHeight() {
			context += fmt.Sprintf("  lines %d-%d/%d", offset+1, end, len(lines))
		}
		if model.contentWidth() < 48 {
			context = "details"
			if len(lines) > model.bodyHeight() {
				context += fmt.Sprintf(" %d-%d/%d", offset+1, end, len(lines))
			}
		}
		return model.chrome(context, strings.Join(lines[offset:end], "\n"), model.detailHelp())
	}
	return model.chrome(model.resultsContext(), model.resultsBody(), model.resultsHelp())
}

func (model Model) resultsContext() string {
	verb := "Found"
	if model.loading {
		verb = "Finding"
	}
	if model.contentWidth() < 48 {
		if model.loading {
			return fmt.Sprintf("finding %d", len(model.visible))
		}
		return fmt.Sprintf("%d groups", len(model.groups))
	}
	return fmt.Sprintf("%s %d files in %d groups  format:%s  sort:%s", verb, len(model.visible), len(model.groups), model.format, model.sortName())
}

func (model Model) resultsBody() string {
	lines := make([]string, 0, model.bodyHeight())
	if len(model.providerStatus) > 0 {
		names := make([]string, 0, len(model.providerStatus))
		for name := range model.providerStatus {
			names = append(names, name)
		}
		sort.Strings(names)
		statuses := make([]string, 0, len(names))
		for _, name := range names {
			statuses = append(statuses, name+": "+model.providerStatus[name])
		}
		lines = append(lines, model.faint.Render(truncate(strings.Join(statuses, "  "), model.contentWidth())))
	}
	if model.filtering || model.filter != "" {
		cursor := ""
		if model.filtering {
			cursor = "_"
		}
		lines = append(lines, truncate("Filter: "+model.filter+cursor, model.contentWidth()))
	}
	remaining := model.bodyHeight() - len(lines)
	if model.status != "" {
		remaining--
	}
	if len(model.groups) == 0 {
		lines = append(lines, "", "  No matching results.")
	} else if remaining > 0 {
		lines = append(lines, model.resultWindow(remaining)...)
	}
	if model.status != "" {
		lines = append(lines, model.accent.Render(truncate(model.status, model.contentWidth())))
	}
	return strings.Join(lines, "\n")
}

func (model Model) renderBrand() string {
	return model.faint.Render("(´∀｀)") + "  " + model.brand.Render("文字  moji")
}

const maxContentWidth = 112

func (model Model) termWidth() int {
	if model.width <= 0 {
		return 100
	}
	return model.width
}

func (model Model) termHeight() int {
	if model.height <= 0 {
		return 30
	}
	return model.height
}

func (model Model) contentWidth() int {
	width := model.termWidth()
	if width >= 32 {
		width -= 4
	}
	return min(maxContentWidth, max(1, width))
}

func (model Model) bodyHeight() int { return max(1, model.termHeight()-4) }

func (model Model) center(block string) string {
	padding := max(0, (model.termWidth()-model.contentWidth())/2)
	if padding == 0 {
		return block
	}
	prefix := strings.Repeat(" ", padding)
	lines := strings.Split(block, "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n")
}

func (model Model) centerLine(line string, boundaryWidth int) string {
	line = truncate(line, boundaryWidth)
	left := max(0, (model.termWidth()-lipgloss.Width(line))/2)
	return strings.Repeat(" ", left) + line
}

func (model Model) chrome(context, body, help string) string {
	width := model.contentWidth()
	header := model.renderBrand() + model.faint.Render("  "+context)
	if width < 34 {
		header = model.brand.Render("moji") + model.faint.Render("  "+context)
	}
	header = truncate(header, width)
	rule := model.faint.Render(strings.Repeat("─", width))
	bodyLines := strings.Split(body, "\n")
	if body == "" {
		bodyLines = nil
	}
	if len(bodyLines) > model.bodyHeight() {
		bodyLines = bodyLines[:model.bodyHeight()]
	}
	for len(bodyLines) < model.bodyHeight() {
		bodyLines = append(bodyLines, "")
	}
	for index := range bodyLines {
		bodyLines[index] = truncate(bodyLines[index], width)
	}
	parts := []string{padRight(header, width), rule, strings.Join(bodyLines, "\n"), rule, truncate(help, width)}
	return model.center(strings.Join(parts, "\n"))
}

func (model Model) resultWindow(height int) []string {
	rowsPerResult := 1
	if model.contentWidth() < 72 {
		rowsPerResult = 2
	}
	capacity := max(1, height/rowsPerResult)
	return renderWindow(&model.resultsWindow, len(model.groups), capacity, func(index int, selected bool) []string {
		return model.groupRow(model.groups[index], selected)
	})
}

func (model Model) resultPageSize() int {
	height := model.bodyHeight()
	if model.status != "" {
		height--
	}
	if len(model.providerStatus) > 0 {
		height--
	}
	if model.filtering || model.filter != "" {
		height--
	}
	if model.contentWidth() < 72 {
		height /= 2
	}
	return max(1, height)
}

func (model Model) resultRow(result provider.Result, selected bool) []string {
	width := model.contentWidth()
	prefix := "  "
	if selected {
		prefix = "> "
	}
	decorate := func(line string) string {
		line = truncate(line, width)
		if selected {
			return model.accent.Render(line)
		}
		return line
	}
	format := displayFormat(result)
	if width >= 72 {
		formatWidth, weightWidth := 7, 11
		sourceWidth := min(28, max(12, width/4))
		nameWidth := max(8, width-2-formatWidth-weightWidth-sourceWidth-6)
		line := prefix + padRight(truncate(result.Filename, nameWidth), nameWidth) + "  " +
			padRight(model.formatBadge(format), formatWidth) + " " +
			padRight(truncate(displayWeight(result.Weight), weightWidth), weightWidth) + " " +
			truncate(model.providerTag(result.Source), sourceWidth)
		return []string{decorate(line)}
	}
	name := prefix + truncate(result.Filename, max(1, width-2))
	metadata := "  " + model.formatBadge(format) + "  " + displayWeight(result.Weight)
	if width >= 42 && result.Source != "" {
		metadata += "  " + model.providerTag(result.Source)
	}
	return []string{decorate(name), decorate(model.faint.Render(truncate(metadata, width)))}
}

func (model Model) groupRow(group rank.ResultGroup, selected bool) []string {
	if group.FileCount == 1 {
		return model.resultRow(group.Files[0], selected)
	}
	width := model.contentWidth()
	prefix := "  "
	if selected {
		prefix = "> "
	}
	name := group.FamilyName
	if name == "" {
		name = group.Files[0].Filename
	} else {
		name = titleFamily(name)
	}
	formats := strings.ToUpper(strings.Join(group.Formats, "/"))
	if width < 72 {
		nameLine := prefix + truncate(name, max(1, width-2))
		metadata := fmt.Sprintf("  %d files  %s  %s", group.FileCount, model.formatBadge(formats), model.providerTag(group.Source))
		if selected {
			return []string{model.accent.Render(truncate(nameLine, width)), model.accent.Render(truncate(metadata, width))}
		}
		return []string{truncate(nameLine, width), model.faint.Render(truncate(metadata, width))}
	}
	line := fmt.Sprintf("%s%s  %d files  %s  %s", prefix, name, group.FileCount, model.formatBadge(formats), model.providerTag(group.Source))
	line = truncate(line, width)
	if selected {
		line = model.accent.Render(line)
	}
	return []string{line}
}

func (model Model) detailLines(result provider.Result) []string {
	width := model.contentWidth()
	nameLines := wrapCells(result.Filename, width)
	lines := make([]string, len(nameLines))
	for index := range nameLines {
		lines[index] = model.accent.Render(nameLines[index])
	}
	fields := [][2]string{{"Format", model.formatBadge(displayFormat(result))}, {"Weight", displayWeight(result.Weight)}, {"Source", model.providerTag(result.Source)}, {"License", model.licenseBadge(result.License)}, {"URL", result.URL}}
	for _, field := range fields {
		lines = append(lines, wrapCells(field[0]+": "+field[1], width)...)
	}
	return lines
}

func (model Model) resultsHelp() string {
	switch {
	case model.contentWidth() < 34:
		return model.faint.Render("j/k move  enter view  q")
	case model.contentWidth() < 48:
		return model.faint.Render("j/k move  enter view  q quit")
	case model.contentWidth() < 64:
		return model.faint.Render("j/k browse  enter preview  / filter  tab health  q quit")
	case model.contentWidth() < 90:
		return model.faint.Render("j/k browse  enter preview  D get  / filter  tab health  q quit")
	default:
		return model.faint.Render("up/down: browse  pgup/dn: page  enter: preview  D: download  /: filter  f: format  o: sort  tab: health  q: quit")
	}
}

func (model Model) detailHelp() string {
	if model.contentWidth() < 34 {
		return model.faint.Render("j/k scroll  esc back")
	}
	if model.contentWidth() < 48 {
		return model.faint.Render("j/k scroll  D download  esc back")
	}
	return model.faint.Render("up/down: scroll  D: download  esc: back  q: quit")
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return ansi.Truncate(value, width, "")
	}
	return ansi.Truncate(value, width, "...")
}

func padRight(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func wrapCells(value string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for value != "" {
		if lipgloss.Width(value) <= width {
			lines = append(lines, value)
			break
		}
		cut := min(width, len(value))
		for cut > 0 && cut < len(value) && !utf8.RuneStart(value[cut]) {
			cut--
		}
		for cut > 0 && lipgloss.Width(value[:cut]) > width {
			_, size := utf8.DecodeLastRuneInString(value[:cut])
			cut -= size
		}
		if cut == 0 {
			_, cut = utf8.DecodeRuneInString(value)
		} else if cut < len(value) {
			// Prefer the last word boundary that still uses most of the row. This
			// avoids visibly splitting prose such as provider warnings while still
			// allowing long URLs and filenames to make progress.
			if boundary := strings.LastIndexByte(value[:cut], ' '); boundary >= cut/2 {
				cut = boundary + 1
			}
		}
		lines = append(lines, strings.TrimRight(value[:cut], " "))
		value = strings.TrimLeft(value[cut:], " ")
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (model Model) waitForEvent() tea.Cmd {
	if model.events == nil {
		return nil
	}
	return func() tea.Msg {
		event, open := <-model.events
		return eventMessage{event: event, open: open}
	}
}

func eventStatus(event provider.Event) string {
	switch event.Status {
	case provider.StateSearching:
		return "searching"
	case provider.StateDone:
		return fmt.Sprintf("done (%d results)", event.Count)
	case provider.StateThrottled:
		if event.RetryAfter > 0 {
			return fmt.Sprintf("rate limited (retrying in %s)", event.RetryAfter)
		}
		return "rate limited"
	case provider.StateFailed:
		return provider.DescribeFailure(event.Provider, event.Err)
	default:
		return "unknown"
	}
}

func (model *Model) refresh() {
	model.visible = model.visible[:0]
	filter := strings.ToLower(model.filter)
	for _, result := range rank.Results(model.all, model.query, model.wantedWeight, model.ranking) {
		weight := rank.WeightOf(result)
		if model.wantedWeight != "" && weight != model.wantedWeight {
			continue
		}
		if model.format != "all" && result.Format != model.format {
			continue
		}
		searchable := strings.ToLower(result.Filename + " " + result.Weight + " " + result.Source)
		if filter != "" && !strings.Contains(searchable, filter) {
			continue
		}
		model.visible = append(model.visible, result)
		if model.maximum > 0 && len(model.visible) == model.maximum {
			break
		}
	}
	switch model.sortMode {
	case 1:
		sort.SliceStable(model.visible, func(i, j int) bool { return model.visible[i].Format < model.visible[j].Format })
	case 2:
		sort.SliceStable(model.visible, func(i, j int) bool { return model.visible[i].SizeBytes < model.visible[j].SizeBytes })
	}
	model.groups = rank.Groups(model.visible)
	model.resultsWindow.clamp(len(model.groups), model.resultPageSize())
	if len(model.visible) == 0 {
		if model.screen == screenPreview {
			model.screen = screenResults
		}
		model.detailOffset = 0
	}
}

func (model Model) currentGroup() rank.ResultGroup {
	if len(model.groups) == 0 {
		return rank.ResultGroup{}
	}
	index := min(len(model.groups)-1, max(0, model.resultsWindow.cursor))
	return model.groups[index]
}

func (model Model) previewIsFamily() bool { return len(model.currentGroup().Files) > 1 }

func (model *Model) selectAllCurrentGroup() {
	model.selectedFiles = make(map[int]bool, len(model.currentGroup().Files))
	for index := range model.currentGroup().Files {
		model.selectedFiles[index] = true
	}
}

func (model Model) selectedCount() int {
	count := 0
	for index := range model.currentGroup().Files {
		if model.selectedFiles[index] {
			count++
		}
	}
	return count
}

func (model Model) downloadSelected() tea.Cmd {
	group := model.currentGroup()
	selected := make([]provider.Result, 0, len(group.Files))
	for index, result := range group.Files {
		if model.selectedFiles[index] {
			selected = append(selected, result)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return func() tea.Msg {
		paths := make([]string, 0, len(selected))
		for _, result := range selected {
			path, err := model.downloader(result)
			if err != nil {
				return downloadMessage{paths: paths, err: err}
			}
			paths = append(paths, path)
		}
		return downloadMessage{paths: paths}
	}
}

func (model Model) previewContext() string {
	group := model.currentGroup()
	if model.contentWidth() < 34 {
		return fmt.Sprintf("preview %d/%d", model.selectedCount(), len(group.Files))
	}
	return fmt.Sprintf("family preview  %d/%d selected", model.selectedCount(), len(group.Files))
}

func (model Model) familyPreviewBody() string {
	group := model.currentGroup()
	available := max(1, model.bodyHeight()-2)
	lines := []string{truncate(fmt.Sprintf("  %s  %s", titleFamily(group.FamilyName), model.providerTag(group.Source)), model.contentWidth()), ""}
	lines = append(lines, renderWindow(&model.previewWindow, len(group.Files), available, func(index int, active bool) []string {
		result := group.Files[index]
		check := "[ ]"
		if model.selectedFiles[index] {
			check = "[x]"
		}
		prefix := "  "
		if active {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%s %s  %s  %s", prefix, check, result.Filename, model.formatBadge(displayFormat(result)), displayWeight(result.Weight))
		line = truncate(line, model.contentWidth())
		if active {
			line = model.accent.Render(line)
		}
		return []string{line}
	})...)
	return strings.Join(lines, "\n")
}

func (model Model) familyPreviewHelp() string {
	if model.contentWidth() < 34 {
		return model.faint.Render("j/k  space  D  esc")
	}
	if model.contentWidth() < 48 {
		return model.faint.Render("j/k move  space select  D get  esc")
	}
	if model.contentWidth() < 72 {
		return model.faint.Render("j/k browse  space select  D download  esc back")
	}
	return model.faint.Render("up/down: browse  space: select  a/n: all/none  D: download  esc: back")
}

func (model Model) confirmHelp() string {
	if model.contentWidth() < 34 {
		return model.faint.Render("y yes  n no")
	}
	return model.faint.Render("y/enter: download  n/esc: cancel")
}

func titleFamily(value string) string {
	words := strings.Fields(value)
	for index, word := range words {
		runes := []rune(word)
		if len(runes) > 0 {
			runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
			words[index] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

func (model Model) providerTag(source string) string {
	name := strings.ToLower(source)
	for _, providerName := range []string{"github", "getfonts", "registry", "websearch", "plugins"} {
		if strings.Contains(name, providerName) {
			label := "[" + providerName + "]"
			if style, ok := model.providerStyles[providerName]; ok {
				return style.Render(label)
			}
			return label
		}
	}
	return source
}

func (model Model) formatBadge(format string) string {
	return model.faint.Render("[" + strings.ToLower(format) + "]")
}

func (model Model) licenseBadge(license string) string {
	if license == "" {
		return model.warning.Render("unknown")
	}
	lower := strings.ToLower(license)
	if strings.Contains(lower, "ofl") || strings.Contains(lower, "apache") {
		return model.success.Render(license)
	}
	if strings.Contains(lower, "commercial") || strings.Contains(lower, "personal") {
		return model.danger.Render(license)
	}
	return model.warning.Render(license)
}

func (model Model) healthBody() string {
	if len(model.providerStatus) == 0 {
		return "\n  No provider activity yet. Start a search to collect health information."
	}
	names := make([]string, 0, len(model.providerStatus))
	for name := range model.providerStatus {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names)+2)
	lines = append(lines, model.faint.Render("  Provider        Latest state"), "")
	for _, name := range names {
		state := model.providerStatus[name]
		dot := model.healthDot(state)
		lines = append(lines, truncate(fmt.Sprintf("  %s  %-14s %s", dot, name, state), model.contentWidth()))
	}
	if model.status != "" {
		lines = append(lines, "", model.warning.Render(truncate(model.status, model.contentWidth())))
	}
	return strings.Join(lines, "\n")
}

func (model Model) healthDot(state string) string {
	switch {
	case strings.HasPrefix(state, "done"):
		return model.success.Render("●")
	case strings.Contains(state, "failed"), strings.Contains(state, "couldn't"):
		return model.danger.Render("●")
	case strings.Contains(state, "searching"), strings.Contains(state, "rate limited"):
		return model.warning.Render("●")
	default:
		return model.faint.Render("●")
	}
}

func (model Model) healthHelp() string {
	if model.contentWidth() < 34 {
		return model.faint.Render("r check  tab back  q")
	}
	if model.contentWidth() < 48 {
		return model.faint.Render("r check  tab/esc back  q quit")
	}
	return model.faint.Render("r: re-check  tab/esc: results  q: quit")
}

func (model Model) sortName() string {
	return []string{"score", "format", "size"}[model.sortMode]
}

func displayWeight(weight string) string {
	if weight == "" {
		return "-"
	}
	return weight
}

func displayFormat(result provider.Result) string {
	format := strings.ToUpper(result.Format)
	if result.Variable {
		format += "-VAR"
	}
	return format
}

func Run(input io.Reader, output io.Writer, model Model) error {
	_, err := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen()).Run()
	return err
}
