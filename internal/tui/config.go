package tui

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/microck/moji/internal/config"
)

type configField struct {
	label   string
	value   string
	secret  bool
	boolean bool
}

type ConfigModel struct {
	path    string
	current config.Config
	fields  []configField
	cursor  int
	editing bool
	status  string
	saved   bool
	brand   lipgloss.Style
	accent  lipgloss.Style
	faint   lipgloss.Style
	warning lipgloss.Style
	width   int
	height  int
}

func NewConfigModel(current config.Config, path string, color bool) ConfigModel {
	providers := []string{"github", "getfonts", "registry", "plugins", "websearch"}
	fields := []configField{
		{label: "Download directory", value: current.DownloadDir},
		{label: "GitHub token", value: current.GitHubToken, secret: true},
		{label: "Search timeout (seconds)", value: strconv.Itoa(current.SearchTimeoutSeconds)},
		{label: "Cache TTL (seconds)", value: strconv.Itoa(current.CacheTTLSeconds)},
		{label: "Default formats", value: strings.Join(current.DefaultFormats, ", ")},
	}
	for _, name := range providers {
		setting := current.Providers[name]
		fields = append(fields, configField{label: name + " enabled", value: strconv.FormatBool(setting.Enabled), boolean: true})
		if name == "github" || name == "getfonts" || name == "websearch" {
			fields = append(fields, configField{label: name + " instance", value: setting.Instance})
		}
	}
	fields = append(fields, configField{label: "Source plugins", value: strings.Join(current.SourcePlugins, ", ")})
	fields = append(fields,
		configField{label: "Ranking: format", value: formatFloat(current.Ranking.Format)},
		configField{label: "Ranking: family size", value: formatFloat(current.Ranking.FamilySize)},
		configField{label: "Ranking: trusted", value: formatFloat(current.Ranking.Trusted)},
		configField{label: "Ranking: size penalty", value: formatFloat(current.Ranking.SizePenalty)},
		configField{label: "Ranking: weight bonus", value: formatFloat(current.Ranking.WeightBonus)},
		configField{label: "Ranking: variable bonus", value: formatFloat(current.Ranking.VariableBonus)},
	)
	for _, name := range providers {
		policy := current.RateLimits[name]
		fields = append(fields,
			configField{label: name + " timeout", value: strconv.Itoa(policy.TimeoutSeconds)},
			configField{label: name + " retries", value: strconv.Itoa(policy.Retries)},
		)
	}
	model := ConfigModel{path: path, current: current, fields: fields}
	if color {
		model.brand = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8C00")).Bold(true)
		model.accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
		model.faint = lipgloss.NewStyle().Foreground(lipgloss.Color("#777777"))
		model.warning = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F"))
	}
	return model
}

func (model ConfigModel) Init() tea.Cmd { return nil }

func (model ConfigModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		if message.Width > 0 {
			model.width = message.Width
		}
		if message.Height > 0 {
			model.height = message.Height
		}
		return model, nil
	case tea.KeyMsg:
		if model.editing {
			switch message.String() {
			case "esc":
				model.editing = false
			case "enter":
				model.editing = false
				model.status = ""
			case "backspace":
				runes := []rune(model.fields[model.cursor].value)
				if len(runes) > 0 {
					model.fields[model.cursor].value = string(runes[:len(runes)-1])
				}
			default:
				if len(message.Runes) > 0 {
					model.fields[model.cursor].value += sanitizeInput(message.Runes)
				}
			}
			return model, nil
		}
		switch message.String() {
		case "ctrl+c", "q", "esc":
			return model, tea.Quit
		case "up", "k":
			model.cursor = max(0, model.cursor-1)
		case "down", "j":
			model.cursor = min(len(model.fields)-1, model.cursor+1)
		case "home", "g":
			model.cursor = 0
		case "end", "G":
			model.cursor = len(model.fields) - 1
		case "enter", " ":
			field := &model.fields[model.cursor]
			if field.boolean {
				field.value = strconv.FormatBool(field.value != "true")
			} else {
				model.editing = true
			}
		case "ctrl+s", "s":
			updated, err := model.config()
			if err != nil {
				model.status = err.Error()
				return model, nil
			}
			if err := config.Save(model.path, updated); err != nil {
				model.status = err.Error()
				return model, nil
			}
			model.current = updated
			model.saved = true
			model.status = "Saved " + model.path
		}
	}
	return model, nil
}

func (model ConfigModel) config() (config.Config, error) {
	updated := model.current
	updated.DownloadDir = strings.TrimSpace(model.fields[0].value)
	updated.GitHubToken = strings.TrimSpace(model.fields[1].value)
	searchTimeout, err := positiveInt(model.fields[2].value, "search timeout")
	if err != nil {
		return config.Config{}, err
	}
	cacheTTL, err := nonNegativeInt(model.fields[3].value, "cache TTL")
	if err != nil {
		return config.Config{}, err
	}
	formats, err := config.ParseFormats(model.fields[4].value)
	if err != nil {
		return config.Config{}, fmt.Errorf("default formats: %w", err)
	}
	updated.SearchTimeoutSeconds = searchTimeout
	updated.CacheTTLSeconds = cacheTTL
	updated.DefaultFormats = formats

	index := 5
	for _, name := range []string{"github", "getfonts", "registry", "plugins", "websearch"} {
		setting := updated.Providers[name]
		setting.Enabled = model.fields[index].value == "true"
		index++
		if name == "github" || name == "getfonts" || name == "websearch" {
			setting.Instance = strings.TrimSpace(model.fields[index].value)
			index++
		}
		updated.Providers[name] = setting
	}
	plugins := strings.TrimSpace(model.fields[index].value)
	updated.SourcePlugins = nil
	if plugins != "" {
		for _, plugin := range strings.Split(plugins, ",") {
			if plugin = strings.TrimSpace(plugin); plugin != "" {
				updated.SourcePlugins = append(updated.SourcePlugins, plugin)
			}
		}
	}
	index++
	rankingValues := []*float64{
		&updated.Ranking.Format, &updated.Ranking.FamilySize, &updated.Ranking.Trusted,
		&updated.Ranking.SizePenalty, &updated.Ranking.WeightBonus, &updated.Ranking.VariableBonus,
	}
	for _, destination := range rankingValues {
		value, err := nonNegativeFloat(model.fields[index].value, model.fields[index].label)
		if err != nil {
			return config.Config{}, err
		}
		*destination = value
		index++
	}
	for _, name := range []string{"github", "getfonts", "registry", "plugins", "websearch"} {
		timeout, err := positiveInt(model.fields[index].value, name+" timeout")
		if err != nil {
			return config.Config{}, err
		}
		index++
		retries, err := nonNegativeInt(model.fields[index].value, name+" retries")
		if err != nil {
			return config.Config{}, err
		}
		index++
		updated.RateLimits[name] = config.RateLimitConfig{TimeoutSeconds: timeout, Retries: retries}
	}
	return updated, nil
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func positiveInt(value, label string) (int, error) {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", label)
	}
	return number, nil
}

func nonNegativeInt(value, label string) (int, error) {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || number < 0 {
		return 0, fmt.Errorf("%s must be 0 or greater", label)
	}
	return number, nil
}

func nonNegativeFloat(value, label string) (float64, error) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("%s must be 0 or greater", label)
	}
	return number, nil
}

func sanitizeInput(runes []rune) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' {
			return ' '
		}
		if character < ' ' {
			return -1
		}
		return character
	}, string(runes))
}

func (model ConfigModel) View() string {
	width, height := model.width, model.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}
	contentWidth := min(112, max(1, width-4))
	bodyHeight := max(1, height-4)
	start := min(max(0, model.cursor-bodyHeight+2), max(0, len(model.fields)-bodyHeight+1))
	end := min(len(model.fields), start+bodyHeight-1)
	lines := make([]string, 0, bodyHeight)
	for index := start; index < end; index++ {
		field := model.fields[index]
		prefix := "  "
		if index == model.cursor {
			prefix = "> "
		}
		value := field.value
		if field.secret && value != "" {
			value = strings.Repeat("*", min(12, len([]rune(value))))
		}
		if field.boolean {
			if value == "true" {
				value = "[x]"
			} else {
				value = "[ ]"
			}
		}
		cursor := ""
		if index == model.cursor && model.editing {
			cursor = "_"
		}
		line := fmt.Sprintf("%s%-28s %s%s", prefix, field.label, value, cursor)
		line = truncate(line, contentWidth)
		if index == model.cursor {
			line = model.accent.Render(line)
		}
		lines = append(lines, line)
	}
	if model.status != "" {
		lines = append(lines, model.warning.Render(truncate(model.status, contentWidth)))
	}
	for len(lines) < bodyHeight {
		lines = append(lines, "")
	}
	header := truncate(model.faint.Render("(´∀｀)")+"  "+model.brand.Render("文字  moji")+model.faint.Render("  configuration"), contentWidth)
	rule := model.faint.Render(strings.Repeat("─", contentWidth))
	help := "up/down: select  enter: edit/toggle  s: save  esc: quit"
	block := strings.Join([]string{padRight(header, contentWidth), rule, strings.Join(lines, "\n"), rule, truncate(model.faint.Render(help), contentWidth)}, "\n")
	padding := max(0, (width-contentWidth)/2)
	if padding == 0 {
		return block
	}
	prefix := strings.Repeat(" ", padding)
	rows := strings.Split(block, "\n")
	for index := range rows {
		rows[index] = prefix + rows[index]
	}
	return strings.Join(rows, "\n")
}

func RunConfig(input io.Reader, output io.Writer, current config.Config, path string, color bool) error {
	_, err := tea.NewProgram(NewConfigModel(current, path, color), tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen()).Run()
	return err
}
