package tui

type listWindow struct {
	cursor int
	offset int
}

func (window *listWindow) move(delta, total, visible int) {
	if total <= 0 {
		window.cursor = 0
		window.offset = 0
		return
	}
	window.cursor = min(total-1, max(0, window.cursor+delta))
	window.clamp(total, visible)
}

func (window *listWindow) home() {
	window.cursor = 0
	window.offset = 0
}

func (window *listWindow) end(total, visible int) {
	window.cursor = max(0, total-1)
	window.clamp(total, visible)
}

func (window *listWindow) clamp(total, visible int) (int, int) {
	if total <= 0 {
		window.cursor = 0
		window.offset = 0
		return 0, 0
	}
	visible = max(1, visible)
	window.cursor = min(total-1, max(0, window.cursor))
	maximumOffset := max(0, total-visible)
	window.offset = min(maximumOffset, max(0, window.offset))
	if window.cursor < window.offset {
		window.offset = window.cursor
	}
	if window.cursor >= window.offset+visible {
		window.offset = window.cursor - visible + 1
	}
	return window.offset, min(total, window.offset+visible)
}

func renderWindow(window *listWindow, total, visible int, render func(index int, selected bool) []string) []string {
	start, end := window.clamp(total, visible)
	lines := make([]string, 0, visible)
	for index := start; index < end; index++ {
		lines = append(lines, render(index, index == window.cursor)...)
	}
	return lines
}
