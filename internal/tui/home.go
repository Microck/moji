package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/microck/moji/internal/provider"
	"github.com/microck/moji/internal/rank"
)

type SearchFunc func(query string) (<-chan provider.Event, error)

var fullMona = []string{
	"（　・∀・）",
}

var mojiWordmark = []string{
	"█▀▄▀█ █▀█   █ █",
	"█ ▀ █ █▄█ █▄█ █",
}

func NewHomeModel(search SearchFunc, downloader DownloadFunc, color bool, wantedWeight string, ranking rank.Weights, maximum int, homeHint string) Model {
	model := NewModel(nil, downloader, color)
	model.screen = screenHome
	model.search = search
	model.wantedWeight = wantedWeight
	model.ranking = ranking
	model.maximum = maximum
	model.homeHint = homeHint
	return model
}

func (model Model) updateHome(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "ctrl+c", "esc":
		return model, tea.Quit
	case "enter":
		query := strings.TrimSpace(model.query)
		if query == "" {
			model.status = "Type a font name before searching."
			return model, nil
		}
		if model.search == nil {
			model.status = "Search is unavailable. Exit and try again."
			return model, nil
		}
		events, err := model.search(query)
		if err != nil {
			model.status = "Search couldn't start: " + err.Error()
			return model, nil
		}
		model.screen = screenResults
		model.loading = true
		model.events = events
		model.status = ""
		model.all = nil
		model.visible = nil
		model.resultsWindow.home()
		model.providerStatus = make(map[string]string)
		return model, model.waitForEvent()
	case "backspace":
		runes := []rune(model.query)
		if len(runes) > 0 {
			model.query = string(runes[:len(runes)-1])
		}
	default:
		if len(key.Runes) > 0 {
			// Terminals deliver bracketed paste as one key event, including any
			// copied line breaks. Collapse whitespace so the one-line input cannot
			// spill through its border.
			model.query += strings.Map(func(character rune) rune {
				if unicode.IsSpace(character) {
					return ' '
				}
				if unicode.IsControl(character) {
					return -1
				}
				return character
			}, string(key.Runes))
			model.status = ""
		}
	}
	return model, nil
}

func (model Model) viewHome() string {
	contentWidth := model.contentWidth()
	inputWidth := min(64, contentWidth)
	body := make([]string, 0, 16)
	if model.termHeight() >= 14 {
		body = append(body, model.renderMonaBlock(fullMona)...)
		body = append(body, "")
	}
	if model.termHeight() >= 10 {
		body = append(body,
			model.accent.Bold(true).Render(mojiWordmark[0]),
			model.brand.Render(mojiWordmark[1]),
			"",
		)
	} else {
		body = append(body, model.brand.Render("文字  moji"))
	}
	input := "> " + model.query + "_"
	body = append(body, model.accent.Render("┌"+strings.Repeat("─", max(0, inputWidth-2))+"┐"))
	if inputWidth >= 2 {
		body = append(body, model.accent.Render("│")+padRight(truncate(input, inputWidth-2), inputWidth-2)+model.accent.Render("│"))
	} else {
		body = append(body, truncate(input, inputWidth))
	}
	body = append(body, model.accent.Render("└"+strings.Repeat("─", max(0, inputWidth-2))+"┘"))
	if model.termHeight() >= 12 {
		body = append(body, model.faint.Render("Type a font name, then press Enter."))
	}
	if model.homeHint != "" {
		body = append(body, "")
		for _, line := range wrapCells("[!] "+model.homeHint, contentWidth) {
			if line != "" {
				body = append(body, model.warning.Render(line))
			}
		}
	}
	if model.status != "" {
		body = append(body, model.accent.Render(truncate(model.status, contentWidth)))
	}

	// The home screen is an entry point, not a data view. Keep its small action
	// block centered while preserving the same pinned footer used elsewhere.
	mainHeight := max(1, model.termHeight()-2)
	if len(body) > mainHeight {
		body = body[len(body)-mainHeight:]
	}
	top := max(0, (mainHeight-len(body))/2)
	lines := make([]string, 0, model.termHeight())
	lines = append(lines, make([]string, top)...)
	for _, line := range body {
		lines = append(lines, model.centerLine(line, contentWidth))
	}
	for len(lines) < mainHeight {
		lines = append(lines, "")
	}
	rule := model.centerLine(model.faint.Render(strings.Repeat("─", contentWidth)), contentWidth)
	help := model.centerLine(model.faint.Render("enter: search  esc: quit"), contentWidth)
	return strings.Join(append(lines, rule, help), "\n")
}

func (model Model) renderMonaBlock(lines []string) []string {
	width := 0
	for _, line := range lines {
		width = max(width, lipgloss.Width(line))
	}
	rendered := make([]string, len(lines))
	for index, line := range lines {
		// Pad before centering so every row shares one coordinate system. Centering
		// individual rows would erase the indentation that forms Mona's body.
		rendered[index] = model.faint.Render(padRight(line, width))
	}
	return rendered
}
