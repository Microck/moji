package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWebSearchUsesInstalledKagiBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable uses a POSIX shell")
	}
	executable := filepath.Join(t.TempDir(), "kagi")
	script := `#!/bin/sh
case "$*" in
  *"search --format json --error-format json --limit 20 Basier Narrow font otf zip css"*)
    printf '%s' '{"data":[{"url":"https://github.com/example/fonts/blob/main/BasierNarrow-Regular.otf","title":"Basier Narrow font"},{"url":"https://example.test/fonts/basier-narrow","title":"web page"}]}'
    ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 2)
	if err := (WebSearch{KagiExecutable: executable}).Search(context.Background(), "Basier Narrow", []string{"otf"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if len(out) != 1 {
		t.Fatalf("results = %d, want 1", len(out))
	}
	result := (<-out).Result
	if result.Filename != "BasierNarrow-Regular.otf" || result.URL != "https://raw.githubusercontent.com/example/fonts/main/BasierNarrow-Regular.otf" {
		t.Fatalf("result = %#v", result)
	}
}
