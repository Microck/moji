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
	providerStates map[string]provider.State
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
	secondary      lipgloss.Style
	selection      lipgloss.Style
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

type previewSnapshot struct {
	groupID, activeFile string
	selectedFiles       map[string]bool
}

func NewModel(results []provider.Result, downloader DownloadFunc, color bool) Model {
	model := Model{
		all: append([]provider.Result(nil), results...), downloader: downloader,
		format: "all", providerStatus: make(map[string]string), providerStates: make(map[string]provider.State), ranking: rank.DefaultWeights(), screen: screenResults,
		selectedFiles: make(map[int]bool),
		selection:     lipgloss.NewStyle().Reverse(true),
	}
	if color {
		model.brand = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8C00")).Bold(true)
		model.accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
		model.faint = lipgloss.NewStyle().Foreground(lipgloss.Color("#858585"))
		model.secondary = lipgloss.NewStyle().Foreground(lipgloss.Color("#B0B0B0"))
		model.warning = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F")).Bold(true)
		model.success = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FAF5F"))
		model.danger = lipgloss.NewStyle().Foreground(lipgloss.Color("#D75F5F"))
		model.selection = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500")).
			Bold(true)
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
			event.Result.Provider = event.Provider
			model.all = provider.UniqueResults(append(model.all, event.Result))
			model.refresh()
		} else {
			model.providerStatus[event.Provider] = eventStatus(event)
			model.providerStates[event.Provider] = event.Status
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
				model.providerStates = make(map[string]provider.State)
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
				model.selectAllCurrentGroup()
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
				model.selectRecommendedCurrentGroup()
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
	if model.contentWidth() < 34 {
		return "results"
	}
	return "search results"
}

func (model Model) resultsBody() string {
	lines := model.resultsPrelude()
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

func (model Model) resultsPrelude() []string {
	query := model.query
	if query == "" {
		query = "all fonts"
	}
	lines := []string{truncate(model.faint.Render("Query  ")+model.secondary.Render(query), model.contentWidth())}
	compactFilter := model.bodyHeight() < 6 && (model.filtering || model.filter != "")
	compactStatus := model.bodyHeight() < 6 && model.status != ""
	if !compactFilter && !compactStatus {
		lines = append(lines, model.resultsToolbar())
	}
	if model.bodyHeight() >= 7 {
		if recommendation := model.resultsRecommendation(); recommendation != "" {
			lines = append(lines, recommendation)
		}
	}
	if model.loading && model.bodyHeight() >= 7 {
		if progress := model.providerProgress(); progress != "" {
			lines = append(lines, model.warning.Render(progress))
		}
	} else if model.bodyHeight() >= 7 {
		attention := model.providerAttention()
		if attention != "" {
			lines = append(lines, model.warning.Render(attention))
		}
	}
	if model.filtering || model.filter != "" {
		cursor := ""
		if model.filtering {
			cursor = "_"
		}
		lines = append(lines, truncate("Filter  "+model.filter+cursor, model.contentWidth()))
	}
	if len(model.groups) > 0 && !newResultsLayout(model.contentWidth()).stacked && model.bodyHeight()-len(lines) >= 2 {
		lines = append(lines, model.resultsHeading())
	}
	return lines
}

func (model Model) resultsToolbar() string {
	left := fmt.Sprintf("%d options  %d files", len(model.groups), len(model.visible))
	if model.contentWidth() < 52 {
		return model.secondary.Render(truncate(left, model.contentWidth()))
	}
	right := fmt.Sprintf("Format %s  Order %s", displayFormatFilter(model.format), displaySort(model.sortName()))
	return model.secondary.Render(joinSides(left, right, model.contentWidth()))
}

func (model Model) resultsRecommendation() string {
	if model.sortMode != 0 || len(model.groups) == 0 {
		return ""
	}
	group := model.groups[0]
	format := strings.ToUpper(strings.Join(group.Formats, "/"))
	label := "Recommended"
	if model.loading {
		label = "Best so far"
	}
	description := ""
	if group.FileCount > 1 {
		description = fmt.Sprintf("%d-file %s family - match + coverage", group.FileCount, format)
	} else {
		description = fmt.Sprintf("%s file - strongest match", format)
	}
	if model.contentWidth() < 52 {
		if !model.loading {
			label = "Best"
		}
		if group.FileCount > 1 {
			description = fmt.Sprintf("%d-file family", group.FileCount)
		} else {
			description = format + " file"
		}
	}
	return truncate(model.accent.Render(label)+model.secondary.Render("  "+description+"  ")+model.providerTag(group.Provider), model.contentWidth())
}

func (model Model) providerProgress() string {
	if len(model.providerStates) == 0 {
		return "Searching providers"
	}
	finished := 0
	for _, state := range model.providerStates {
		if state == provider.StateDone || state == provider.StateFailed {
			finished++
		}
	}
	return fmt.Sprintf("Searching providers  %d/%d finished", finished, len(model.providerStates))
}

func (model Model) providerAttention() string {
	count := 0
	for _, state := range model.providerStates {
		if state == provider.StateFailed || state == provider.StateThrottled {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	noun := "providers need"
	if count == 1 {
		noun = "provider needs"
	}
	return fmt.Sprintf("%d %s attention - open Health", count, noun)
}

func (model Model) renderBrand() string {
	return model.faint.Render("(´∀｀)") + "  " + model.brand.Render("文字  moji")
}

const maxContentWidth = 118

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
	switch {
	case width >= 80:
		width -= 8
	case width >= 32:
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
		bodyLines[index] = padRight(truncate(bodyLines[index], width), width)
	}
	parts := []string{padRight(header, width), rule, strings.Join(bodyLines, "\n"), rule, padRight(truncate(help, width), width)}
	return model.center(strings.Join(parts, "\n"))
}

type resultsLayout struct {
	stacked                                         bool
	nameWidth, countWidth, formatWidth, sourceWidth int
}

type previewLayout struct {
	stacked                             bool
	nameWidth, formatWidth, weightWidth int
}

func newResultsLayout(width int) resultsLayout {
	layout := resultsLayout{stacked: width < 52, countWidth: 5, formatWidth: 9}
	if layout.stacked {
		return layout
	}
	layout.sourceWidth = 10
	// Two cells are reserved for the cursor and three spaces separate the four
	// columns. The family name absorbs every remaining cell.
	layout.nameWidth = max(8, width-2-layout.countWidth-layout.formatWidth-layout.sourceWidth-3)
	return layout
}

func newPreviewLayout(width int) previewLayout {
	layout := previewLayout{stacked: width < 52, formatWidth: 8, weightWidth: 10}
	if !layout.stacked {
		// The cursor, checkbox, and three separators consume eight cells.
		layout.nameWidth = max(8, width-8-layout.formatWidth-layout.weightWidth)
	}
	return layout
}

func (model Model) resultsHeading() string {
	layout := newResultsLayout(model.contentWidth())
	line := "  " + padRight("Family", layout.nameWidth) + " " +
		padLeft("Files", layout.countWidth) + " " +
		padRight("Format", layout.formatWidth) + " " +
		padRight("Provider", layout.sourceWidth)
	return model.secondary.Render(padRight(truncate(line, model.contentWidth()), model.contentWidth()))
}

func (model Model) previewHeading(layout previewLayout) string {
	line := "      " + padRight("File", layout.nameWidth) + " " +
		padRight("Format", layout.formatWidth) + " " +
		padRight("Weight", layout.weightWidth)
	return model.secondary.Render(padRight(truncate(line, model.contentWidth()), model.contentWidth()))
}

func (model Model) previewFileRows(layout previewLayout, prefix, check string, result provider.Result, active bool) []string {
	format := strings.ToUpper(displayFormat(result))
	weight := displayWeight(result.Weight)
	if layout.stacked {
		nameLine := padRight(truncate(prefix+check+" "+result.Filename, model.contentWidth()), model.contentWidth())
		metadataLine := padRight(truncate("      "+padRight(format, layout.formatWidth)+" "+weight, model.contentWidth()), model.contentWidth())
		if active {
			return []string{model.accent.Render(nameLine), model.accent.Render(metadataLine)}
		}
		return []string{nameLine, model.secondary.Render(metadataLine)}
	}
	line := prefix + check + " " +
		padRight(truncate(result.Filename, layout.nameWidth), layout.nameWidth) + " " +
		padRight(truncate(format, layout.formatWidth), layout.formatWidth) + " " +
		padRight(truncate(weight, layout.weightWidth), layout.weightWidth)
	line = padRight(truncate(line, model.contentWidth()), model.contentWidth())
	if active {
		return []string{model.accent.Render(line)}
	}
	return []string{line}
}

func (model Model) compactPreviewFileRow(prefix, check string, result provider.Result, active bool) []string {
	format := strings.ToUpper(displayFormat(result))
	weight := displayWeight(result.Weight)
	formatWidth := min(5, lipgloss.Width(format))
	weightWidth := min(8, lipgloss.Width(weight))
	fixedWidth := lipgloss.Width(prefix) + lipgloss.Width(check) + 3 + formatWidth + weightWidth
	nameWidth := max(1, model.contentWidth()-fixedWidth)
	line := prefix + check + " " +
		padRight(truncate(result.Filename, nameWidth), nameWidth) + " " +
		padRight(truncate(format, formatWidth), formatWidth) + " " +
		padRight(truncate(weight, weightWidth), weightWidth)
	line = padRight(truncate(line, model.contentWidth()), model.contentWidth())
	if active {
		line = model.accent.Render(line)
	}
	return []string{line}
}

func (model Model) resultWindow(height int) []string {
	layout := newResultsLayout(model.contentWidth())
	if layout.stacked && height < 2 {
		start, _ := model.resultsWindow.clamp(len(model.groups), 1)
		if len(model.groups) == 0 || height < 1 {
			return nil
		}
		return []string{model.compactResultRow(model.groups[start], start == model.resultsWindow.cursor)}
	}
	rowsPerResult := 1
	if layout.stacked {
		rowsPerResult = 2
	}
	capacity := max(1, height/rowsPerResult)
	lines := renderWindow(&model.resultsWindow, len(model.groups), capacity, func(index int, selected bool) []string {
		return model.groupRow(model.groups[index], selected)
	})
	return lines[:min(len(lines), max(0, height))]
}

func (model Model) resultPageSize() int {
	height := model.bodyHeight() - len(model.resultsPrelude())
	if model.status != "" {
		height--
	}
	if newResultsLayout(model.contentWidth()).stacked {
		height /= 2
	}
	return max(1, height)
}

func (model Model) groupRow(group rank.ResultGroup, selected bool) []string {
	name := group.FamilyName
	if name == "" {
		name = group.Files[0].Filename
	} else {
		name = titleFamily(name)
	}
	return model.renderResultRow(name, group.FileCount, strings.ToUpper(strings.Join(group.Formats, "/")), group.Provider, selected)
}

func (model Model) renderResultRow(name string, count int, format, providerName string, selected bool) []string {
	width := model.contentWidth()
	layout := newResultsLayout(width)
	cursor := " "
	if selected {
		cursor = ">"
	}
	prefix := cursor + " "
	plainProvider := displayProvider(providerName)
	if layout.stacked {
		nameLine := prefix + truncate(name, max(1, width-lipgloss.Width(prefix)))
		metadata := "  " + padLeft(fmt.Sprintf("%d", count), layout.countWidth) + " " + padRight(format, layout.formatWidth)
		if width >= 34 && plainProvider != "" {
			metadata += " " + plainProvider
		}
		rows := []string{padRight(truncate(nameLine, width), width), padRight(truncate(metadata, width), width)}
		if selected {
			for index := range rows {
				rows[index] = model.selection.Render(rows[index])
			}
		} else {
			metadataLine := model.secondary.Render("  " + padLeft(fmt.Sprintf("%d", count), layout.countWidth) + " " + padRight(format, layout.formatWidth))
			if width >= 34 && plainProvider != "" {
				sourceWidth := max(1, width-lipgloss.Width(metadataLine)-1)
				metadataLine += " " + model.renderProvider(providerName, sourceWidth)
			}
			rows[1] = padRight(truncate(metadataLine, width), width)
		}
		return rows
	}
	nameCell := padRight(truncate(name, layout.nameWidth), layout.nameWidth)
	plainLine := prefix + nameCell + " " +
		padLeft(fmt.Sprintf("%d", count), layout.countWidth) + " " +
		padRight(truncate(format, layout.formatWidth), layout.formatWidth) + " " +
		padRight(truncate(plainProvider, layout.sourceWidth), layout.sourceWidth)
	plainLine = padRight(truncate(plainLine, width), width)
	if selected {
		return []string{model.selection.Render(plainLine)}
	}
	line := prefix + nameCell + " " +
		padLeft(fmt.Sprintf("%d", count), layout.countWidth) + " " +
		padRight(truncate(format, layout.formatWidth), layout.formatWidth) + " " +
		model.renderProvider(providerName, layout.sourceWidth)
	line = padRight(truncate(line, width), width)
	return []string{line}
}

func (model Model) compactResultRow(group rank.ResultGroup, selected bool) string {
	name := titleFamily(group.FamilyName)
	if name == "" {
		name = group.Files[0].Filename
	}
	prefix := "  "
	if selected {
		prefix = "> "
	}
	line := padRight(truncate(prefix+name, model.contentWidth()), model.contentWidth())
	if selected {
		return model.selection.Render(line)
	}
	return line
}

func (model Model) detailLines(result provider.Result) []string {
	width := model.contentWidth()
	nameLines := wrapCells(result.Filename, width)
	lines := make([]string, len(nameLines))
	for index := range nameLines {
		lines[index] = model.accent.Render(nameLines[index])
	}
	fields := [][2]string{{"Format", model.formatBadge(displayFormat(result))}, {"Weight", displayWeight(result.Weight)}, {"Provider", model.providerTag(result.Provider)}, {"License", model.licenseBadge(result.License)}, {"URL", result.URL}}
	for _, field := range fields {
		lines = append(lines, wrapCells(field[0]+": "+field[1], width)...)
	}
	return lines
}

func (model Model) resultsHelp() string {
	width := model.contentWidth()
	position := "0/0"
	if len(model.groups) > 0 {
		position = fmt.Sprintf("%d/%d", model.resultsWindow.cursor+1, len(model.groups))
	}
	left := "j/k enter D"
	switch {
	case width >= 90:
		left = "j/k move  enter review  D top file  / filter  f format  o order  H health  q quit"
	case width >= 64:
		left = "j/k move  enter review  D top  / filter  f fmt  o order  H"
	case width >= 48:
		left = "j/k  enter  D  /  f format  o order  H"
	case width >= 34:
		left = "j/k enter D / f o H"
	}
	return model.secondary.Render(joinSides(left, position, width))
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

func padLeft(value string, width int) string {
	return strings.Repeat(" ", max(0, width-lipgloss.Width(value))) + value
}

func joinSides(left, right string, width int) string {
	right = truncate(right, width)
	available := width - lipgloss.Width(right)
	if available <= 1 {
		return padRight(right, width)
	}
	left = truncate(left, available-2)
	gap := max(2, width-lipgloss.Width(left)-lipgloss.Width(right))
	return padRight(truncate(left+strings.Repeat(" ", gap)+right, width), width)
}

func displayFormatFilter(format string) string {
	if format == "" || strings.EqualFold(format, "all") {
		return "All"
	}
	return strings.ToUpper(format)
}

func displaySort(sort string) string {
	if sort == "score" {
		return "Best match"
	}
	if sort == "" {
		return "Best match"
	}
	return strings.ToUpper(sort[:1]) + sort[1:]
}

func displayProvider(providerName string) string {
	if providerName == "" {
		return "unknown"
	}
	return "[" + strings.ToLower(providerName) + "]"
}

func (model Model) renderProvider(providerName string, width int) string {
	providerName = strings.ToLower(providerName)
	label := displayProvider(providerName)
	if style, ok := model.providerStyles[providerName]; ok {
		label = style.Render(label)
	}
	return padRight(truncate(label, width), width)
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
	// Live provider events can reorder both groups and their files. Capture the
	// reviewed identities before ranking again so a numeric cursor never points
	// at a different download after the refresh.
	preview := model.capturePreview()
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
		searchable := strings.ToLower(result.Filename + " " + result.Weight + " " + result.Provider + " " + result.Source)
		if filter != "" && !strings.Contains(searchable, filter) {
			continue
		}
		model.visible = append(model.visible, result)
		if model.maximum > 0 && len(model.visible) == model.maximum {
			break
		}
	}
	model.groups = rank.Groups(model.visible)
	switch model.sortMode {
	case 1:
		sort.SliceStable(model.groups, func(i, j int) bool {
			return model.groups[i].FileCount > model.groups[j].FileCount
		})
	case 2:
		sort.SliceStable(model.groups, func(i, j int) bool {
			left := rank.PreferredFormatOrder(model.groups[i].BestFormat)
			right := rank.PreferredFormatOrder(model.groups[j].BestFormat)
			return left < right
		})
	}
	model.restorePreview(preview)
	model.resultsWindow.clamp(len(model.groups), model.resultPageSize())
	if len(model.visible) == 0 {
		if model.screen == screenPreview {
			model.screen = screenResults
		}
		model.detailOffset = 0
	}
}

func (model Model) capturePreview() previewSnapshot {
	previewOpen := model.screen == screenPreview || model.screen == screenConfirm && model.returnScreen == screenPreview
	if !previewOpen || len(model.groups) == 0 {
		return previewSnapshot{}
	}
	group := model.currentGroup()
	snapshot := previewSnapshot{groupID: group.GroupID, selectedFiles: make(map[string]bool)}
	for index, result := range group.Files {
		if model.selectedFiles[index] {
			snapshot.selectedFiles[provider.ResultIdentity(result)] = true
		}
	}
	if len(group.Files) > 0 {
		activeIndex := min(len(group.Files)-1, max(0, model.previewWindow.cursor))
		snapshot.activeFile = provider.ResultIdentity(group.Files[activeIndex])
	}
	return snapshot
}

func (model *Model) restorePreview(snapshot previewSnapshot) {
	if snapshot.groupID == "" {
		return
	}
	groupIndex := -1
	for index := range model.groups {
		if model.groups[index].GroupID == snapshot.groupID {
			groupIndex = index
			break
		}
	}
	if groupIndex < 0 {
		model.screen = screenResults
		model.detailOffset = 0
		model.previewWindow.home()
		clear(model.selectedFiles)
		return
	}

	model.resultsWindow.cursor = groupIndex
	model.selectedFiles = make(map[int]bool, len(snapshot.selectedFiles))
	for index, result := range model.groups[groupIndex].Files {
		identity := provider.ResultIdentity(result)
		if snapshot.selectedFiles[identity] {
			model.selectedFiles[index] = true
		}
		if identity == snapshot.activeFile {
			model.previewWindow.cursor = index
		}
	}
	model.previewWindow.clamp(len(model.groups[groupIndex].Files), model.bodyHeight()-2)
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

func (model *Model) selectRecommendedCurrentGroup() {
	model.selectedFiles = make(map[int]bool)
	selectedStyles := make(map[string]bool)
	for index, result := range model.currentGroup().Files {
		style := familyStyleCategory(result)
		if selectedStyles[style] {
			continue
		}
		selectedStyles[style] = true
		model.selectedFiles[index] = true
	}
}

func familyStyleCategory(result provider.Result) string {
	tags := rank.ParseFilename(result.Filename)
	posture := "roman"
	if tags.Italic {
		posture = "italic"
	}
	if result.Variable || tags.Variable {
		return "variable\x00" + posture
	}
	weight := rank.WeightOf(result)
	if weight == "" {
		weight = "regular"
	}
	return weight + "\x00" + posture
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
	layout := newPreviewLayout(model.contentWidth())
	lines := []string{truncate(fmt.Sprintf("  %s  %s", titleFamily(group.FamilyName), model.providerTag(group.Provider)), model.contentWidth()), ""}
	if !layout.stacked {
		lines = append(lines, model.previewHeading(layout))
	}
	available := max(1, model.bodyHeight()-len(lines))
	capacity := available
	compact := layout.stacked && available < 2
	if layout.stacked {
		capacity = max(1, available/2)
	}
	lines = append(lines, renderWindow(&model.previewWindow, len(group.Files), capacity, func(index int, active bool) []string {
		result := group.Files[index]
		check := "[ ]"
		if model.selectedFiles[index] {
			check = "[x]"
		}
		prefix := "  "
		if active {
			prefix = "> "
		}
		if compact {
			return model.compactPreviewFileRow(prefix, check, result, active)
		}
		return model.previewFileRows(layout, prefix, check, result, active)
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
	providerName := strings.ToLower(source)
	label := displayProvider(providerName)
	if style, ok := model.providerStyles[providerName]; ok {
		return style.Render(label)
	}
	return label
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
	return []string{"score", "most files", "preferred format"}[model.sortMode]
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
