package fontinstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() { refresh = func(string) {} }

func TestInstallCopiesAndReusesIdenticalFiles(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), filepath.Join(t.TempDir(), "fonts")
	source := filepath.Join(sourceDir, "Example.otf")
	if err := os.WriteFile(source, []byte("OTTO-font"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := Install(fontDir, []string{source}, false, nil)
	if err != nil || len(results) != 1 || results[0].Existing {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	content, err := os.ReadFile(filepath.Join(fontDir, "Example.otf"))
	if err != nil || string(content) != "OTTO-font" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	results, err = Install(fontDir, []string{source}, false, nil)
	if err != nil || !results[0].Existing {
		t.Fatalf("second install results=%+v err=%v", results, err)
	}
}

func TestInstallRefusesToOverwriteDifferentFont(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "Example.otf")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(fontDir, "Example.otf")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Install(fontDir, []string{source}, false, nil)
	var conflict ConflictError
	if !errors.As(err, &conflict) || strings.Join(conflict.Names, ",") != "Example.otf" {
		t.Fatalf("err=%v", err)
	}
	if content, _ := os.ReadFile(existing); string(content) != "old" {
		t.Fatalf("existing font was overwritten: %q", content)
	}
	if _, err := Install(fontDir, []string{source}, true, nil); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(existing); string(content) != "new" {
		t.Fatalf("overwrite kept old content: %q", content)
	}
}

func TestInstallConflictCopiesNothing(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	fresh := filepath.Join(sourceDir, "A.otf")
	clash := filepath.Join(sourceDir, "B.otf")
	for path, content := range map[string]string{fresh: "a", clash: "b", filepath.Join(fontDir, "B.otf"): "old"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install(fontDir, []string{fresh, clash}, false, nil); err == nil {
		t.Fatal("conflict was not reported")
	}
	if _, err := os.Stat(filepath.Join(fontDir, "A.otf")); !os.IsNotExist(err) {
		t.Fatalf("a conflicting install still copied A.otf: %v", err)
	}
}
