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

type Model struct {
	home           bool
	query          string
	search         SearchFunc
	all            []provider.Result
	visible        []provider.Result
	cursor         int
	filter         string
	filtering      bool
	format         string
	sortMode       int
	preview        bool
	detailOffset   int
	status         string
	providerStatus map[string]string
	events         <-chan provider.Event
	loading        bool
	wantedWeight   string
	ranking        rank.Weights
	maximum        int
	downloader     DownloadFunc
	brand          lipgloss.Style
	accent         lipgloss.Style
	faint          lipgloss.Style
	width          int
	height         int
}

type downloadMessage struct {
	path string
	err  error
}

type eventMessage struct {
	event provider.Event
	open  bool
}

func NewModel(results []provider.Result, downloader DownloadFunc, color bool) Model {
	model := Model{
		all: append([]provider.Result(nil), results...), downloader: downloader,
		format: "all", providerStatus: make(map[string]string), ranking: rank.DefaultWeights(),
	}
	if color {
		model.brand = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8C00")).Bold(true)
		model.accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
		model.faint = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	}
	model.refresh()
	return model
}

func NewLiveModel(events <-chan provider.Event, downloader DownloadFunc, color bool, wantedWeight string, ranking rank.Weights, maximum int) Model {
	model := NewModel(nil, downloader, color)
	model.events = events
	model.loading = true
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
	if model.home {
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
			model.status = "Downloaded: " + message.path
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
		if model.preview {
			switch message.String() {
			case "up", "k":
				model.detailOffset = max(0, model.detailOffset-1)
				return model, nil
			case "down", "j":
				maximum := max(0, len(model.detailLines(model.visible[model.cursor]))-model.bodyHeight())
				model.detailOffset = min(maximum, model.detailOffset+1)
				return model, nil
			}
		}
		switch message.String() {
		case "ctrl+c", "q":
			return model, tea.Quit
		case "esc":
			if model.preview {
				model.preview = false
				model.detailOffset = 0
				return model, nil
			}
			return model, tea.Quit
		case "up", "k":
			if model.cursor > 0 {
				model.cursor--
			}
		case "down", "j":
			if model.cursor+1 < len(model.visible) {
				model.cursor++
			}
		case "pgup":
			model.cursor = max(0, model.cursor-model.resultPageSize())
		case "pgdown":
			model.cursor = min(max(0, len(model.visible)-1), model.cursor+model.resultPageSize())
		case "g", "home":
			model.cursor = 0
		case "G", "end":
			model.cursor = max(0, len(model.visible)-1)
		case "enter":
			if len(model.visible) > 0 {
				model.preview = true
				model.detailOffset = 0
			}
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
		case "tab", "o":
			model.sortMode = (model.sortMode + 1) % 3
			model.refresh()
		case "D":
			if len(model.visible) == 0 || model.downloader == nil {
				return model, nil
			}
			selected := model.visible[model.cursor]
			model.status = "Downloading " + selected.Filename + "..."
			return model, func() tea.Msg {
				path, err := model.downloader(selected)
				return downloadMessage{path: path, err: err}
			}
		}
	}
	return model, nil
}

func (model Model) View() string {
	if model.home {
		return model.viewHome()
	}
	if model.preview && len(model.visible) > 0 {
		result := model.visible[model.cursor]
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
		return fmt.Sprintf("%d results", len(model.visible))
	}
	return fmt.Sprintf("%s %d results  format:%s  sort:%s", verb, len(model.visible), model.format, model.sortName())
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
	if len(model.visible) == 0 {
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
	header := truncate(model.renderBrand()+model.faint.Render("  "+context), width)
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
	start := min(max(0, model.cursor-capacity+1), max(0, len(model.visible)-capacity))
	end := min(len(model.visible), start+capacity)
	lines := make([]string, 0, height)
	for index := start; index < end; index++ {
		lines = append(lines, model.resultRow(model.visible[index], index == model.cursor)...)
	}
	return lines
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
	if width >= 72 {
		formatWidth, weightWidth := 6, 11
		sourceWidth := min(28, max(12, width/4))
		nameWidth := max(8, width-2-formatWidth-weightWidth-sourceWidth-6)
		line := prefix + padRight(truncate(result.Filename, nameWidth), nameWidth) + "  " +
			padRight(strings.ToUpper(result.Format), formatWidth) + " " +
			padRight(truncate(displayWeight(result.Weight), weightWidth), weightWidth) + " " +
			truncate(result.Source, sourceWidth)
		return []string{decorate(line)}
	}
	name := prefix + truncate(result.Filename, max(1, width-2))
	metadata := "  " + strings.ToUpper(result.Format) + "  " + displayWeight(result.Weight)
	if width >= 42 && result.Source != "" {
		metadata += "  " + result.Source
	}
	return []string{decorate(name), decorate(model.faint.Render(truncate(metadata, width)))}
}

func (model Model) detailLines(result provider.Result) []string {
	width := model.contentWidth()
	nameLines := wrapCells(result.Filename, width)
	lines := make([]string, len(nameLines))
	for index := range nameLines {
		lines[index] = model.accent.Render(nameLines[index])
	}
	fields := [][2]string{{"Format", strings.ToUpper(result.Format)}, {"Weight", displayWeight(result.Weight)}, {"Source", result.Source}, {"License", result.License}, {"URL", result.URL}}
	for _, field := range fields {
		lines = append(lines, wrapCells(field[0]+": "+field[1], width)...)
	}
	return lines
}

func (model Model) resultsHelp() string {
	switch {
	case model.contentWidth() < 34:
		return model.faint.Render("j/k move  enter open  q")
	case model.contentWidth() < 48:
		return model.faint.Render("j/k move  enter open  q quit")
	case model.contentWidth() < 90:
		return model.faint.Render("up/down browse  enter details  / filter  q quit")
	default:
		return model.faint.Render("up/down: browse  pgup/dn: page  enter: details  D: download  /: filter  f: format  tab: sort  q: quit")
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
		}
		lines = append(lines, value[:cut])
		value = value[cut:]
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
	for _, result := range rank.Results(model.all, model.wantedWeight, model.ranking) {
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
	default:
		sort.SliceStable(model.visible, func(i, j int) bool { return model.visible[i].Score > model.visible[j].Score })
	}
	if model.cursor >= len(model.visible) {
		model.cursor = max(0, len(model.visible)-1)
	}
	if len(model.visible) == 0 {
		model.preview = false
		model.detailOffset = 0
	}
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

func Run(input io.Reader, output io.Writer, model Model) error {
	_, err := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen()).Run()
	return err
}
