package fontinspect

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microck/moji/internal/fontconvert"
)

func TestInspectReadsSupportedFontContainers(t *testing.T) {
	t.Parallel()
	for fixture, expectedFormat := range map[string]string{
		"test-ttf.base64":   "ttf",
		"test-otf.base64":   "otf",
		"test-woff2.base64": "woff2",
	} {
		fixture, expectedFormat := fixture, expectedFormat
		t.Run(expectedFormat, func(t *testing.T) {
			t.Parallel()
			path := writeFixture(t, fixture, "font.bin")

			inspected, err := Inspect(path)
			if err != nil {
				t.Fatal(err)
			}
			if inspected.Path != path || string(inspected.Format) != expectedFormat {
				t.Fatalf("identity = %#v", inspected)
			}
			if inspected.Glyphs <= 0 || inspected.EncodedCharacters <= 0 || inspected.UnicodeVersion == "" {
				t.Fatalf("metadata = %#v", inspected)
			}
			if len(inspected.Scripts) == 0 {
				t.Fatal("scripts are empty")
			}
			scriptCharacters := 0
			for _, script := range inspected.Scripts {
				if script.Name == "" || script.Encoded <= 0 || script.Assigned < script.Encoded ||
					script.Coverage <= 0 || script.Coverage > 100 {
					t.Fatalf("invalid script coverage = %#v", script)
				}
				scriptCharacters += script.Encoded
			}
			if inspected.EncodedCharacters < scriptCharacters {
				t.Fatalf("encoded characters %d are less than script characters %d", inspected.EncodedCharacters, scriptCharacters)
			}
		})
	}
}

func TestInspectCoverageIsStableAcrossTTFAndWOFF2Containers(t *testing.T) {
	t.Parallel()
	ttf, err := Inspect(writeFixture(t, "test-ttf.base64", "font.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	woff2, err := Inspect(writeFixture(t, "test-woff2.base64", "font.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	if ttf.Glyphs != woff2.Glyphs || ttf.EncodedCharacters != woff2.EncodedCharacters {
		t.Fatalf("TTF identity = %#v; WOFF2 identity = %#v", ttf, woff2)
	}
	if len(ttf.Scripts) != len(woff2.Scripts) {
		t.Fatalf("TTF scripts = %#v; WOFF2 scripts = %#v", ttf.Scripts, woff2.Scripts)
	}
	for index := range ttf.Scripts {
		if ttf.Scripts[index] != woff2.Scripts[index] {
			t.Fatalf("script %d differs: TTF=%#v WOFF2=%#v", index, ttf.Scripts[index], woff2.Scripts[index])
		}
	}
}

func TestInspectRejectsOversizedInputBeforeReadingIt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "oversized.ttf")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(fontconvert.MaxSize + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(path); err == nil || !strings.Contains(err.Error(), "inspection limit") {
		t.Fatalf("oversized input error = %v", err)
	}
}

func TestInspectRejectsNonRegularInput(t *testing.T) {
	t.Parallel()
	if _, err := Inspect(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory input error = %v", err)
	}
	if _, err := Inspect(filepath.Join(t.TempDir(), "missing.ttf")); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing input error = %v", err)
	}
}

func TestInspectRejectsFIFOWithoutOpeningIt(t *testing.T) {
	t.Parallel()
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo is unavailable")
	}
	path := filepath.Join(t.TempDir(), "font.ttf")
	if output, err := exec.Command(mkfifo, path).CombinedOutput(); err != nil {
		t.Fatalf("create FIFO: %v: %s", err, output)
	}
	if _, err := Inspect(path); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("FIFO input error = %v", err)
	}
}

func TestInspectRejectsUnsupportedAndInvalidInputs(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		content     []byte
		unsupported bool
	}{
		"font.woff":  {content: []byte("wOFF-not-inspectable"), unsupported: true},
		"font.dfont": {content: []byte("legacy font"), unsupported: true},
		"font.pfb":   {content: []byte("legacy font"), unsupported: true},
		"font.pfm":   {content: []byte("legacy font"), unsupported: true},
		"collection": {content: []byte("ttcf-not-inspectable"), unsupported: true},
		"malformed":  {content: []byte("not a font")},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Inspect(path)
			if err == nil || IsUnsupported(err) != test.unsupported {
				t.Fatalf("Inspect() error = %v, unsupported = %v", err, IsUnsupported(err))
			}
		})
	}
}

func writeFixture(t *testing.T, fixture, name string) string {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "fontconvert", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
