package filecommit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveNoReplacePreservesConcurrentDestination(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.otf")
	destination := filepath.Join(directory, "destination.otf")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("concurrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MoveNoReplace(source, destination); err == nil {
		t.Fatal("MoveNoReplace overwrote an existing destination")
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "concurrent" {
		t.Fatalf("destination=%q err=%v", content, err)
	}
}
