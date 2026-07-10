package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
)

type DownloadFunc func(provider.Result) (string, error)

type Model struct {
	all            []provider.Result
	visible        []provider.Result
	cursor         int
	filter         string
	filtering      bool
	format         string
	sortMode       int
	preview        bool
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
		switch message.String() {
		case "ctrl+c", "q":
			return model, tea.Quit
		case "esc":
			if model.preview {
				model.preview = false
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
		case "enter":
			if len(model.visible) > 0 {
				model.preview = true
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
	var view strings.Builder
	view.WriteString(model.brand.Render("文字  moji"))
	view.WriteString("\n\n")
	if model.preview && len(model.visible) > 0 {
		result := model.visible[model.cursor]
		fmt.Fprintf(&view, "%s\n\nFormat: %s\nWeight: %s\nSource: %s\nLicense: %s\nURL: %s\n\n",
			model.accent.Render(result.Filename), strings.ToUpper(result.Format), displayWeight(result.Weight),
			result.Source, result.License, result.URL)
		view.WriteString(model.faint.Render("D: download  esc: back  q: quit"))
		return view.String()
	}
	verb := "Found"
	if model.loading {
		verb = "Finding"
	}
	fmt.Fprintf(&view, "%s %d results  [format: %s]  [sort: %s]\n", verb, len(model.visible), model.format, model.sortName())
	if len(model.providerStatus) > 0 {
		names := make([]string, 0, len(model.providerStatus))
		for name := range model.providerStatus {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&view, "  %s: %s\n", name, model.providerStatus[name])
		}
	}
	if model.filtering || model.filter != "" {
		cursor := ""
		if model.filtering {
			cursor = "_"
		}
		fmt.Fprintf(&view, "Filter: %s%s\n", model.filter, cursor)
	}
	view.WriteString("\n")
	for index, result := range model.visible {
		prefix := "  "
		line := fmt.Sprintf("%s  %-6s %-10s %-32s", result.Filename, strings.ToUpper(result.Format), displayWeight(result.Weight), result.Source)
		if index == model.cursor {
			prefix = "> "
			line = model.accent.Render(line)
		}
		view.WriteString(prefix + line + "\n")
	}
	if len(model.visible) == 0 {
		view.WriteString("  No matching results.\n")
	}
	if model.status != "" {
		view.WriteString("\n" + model.status + "\n")
	}
	view.WriteString("\n" + model.faint.Render("up/down: browse  enter: details  D: download  /: filter  f: format  tab: sort  q: quit"))
	return view.String()
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
