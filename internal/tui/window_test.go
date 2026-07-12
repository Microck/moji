package tui

import "testing"

func TestListWindowKeepsCursorVisible(t *testing.T) {
	window := listWindow{}
	window.move(7, 10, 3)
	start, end := window.clamp(10, 3)
	if window.cursor != 7 || start != 5 || end != 8 {
		t.Fatalf("cursor=%d range=%d:%d", window.cursor, start, end)
	}
	window.home()
	if start, end = window.clamp(10, 3); window.cursor != 0 || start != 0 || end != 3 {
		t.Fatalf("home cursor=%d range=%d:%d", window.cursor, start, end)
	}
	window.end(10, 3)
	if start, end = window.clamp(10, 3); window.cursor != 9 || start != 7 || end != 10 {
		t.Fatalf("end cursor=%d range=%d:%d", window.cursor, start, end)
	}
}
