package fontconvert

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFontToolsInteroperability is opt-in locally because FontTools and its
// Brotli extension are development or CI dependencies, never Moji runtime
// dependencies. CI enables it through MOJI_FONTTOOLS_CONFORMANCE.
func TestFontToolsInteroperability(t *testing.T) {
	if os.Getenv("MOJI_FONTTOOLS_CONFORMANCE") == "" {
		t.Skip("set MOJI_FONTTOOLS_CONFORMANCE=1 with fonttools[woff] installed")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 is required for enabled FontTools conformance")
	}
	for fixture, format := range map[string]Format{
		"test-ttf.base64": FormatTTF,
		"test-otf.base64": FormatOTF,
	} {
		fixture, format := fixture, format
		t.Run(string(format), func(t *testing.T) {
			directory := t.TempDir()
			source := writeFixture(t, directory, fixture, "source."+string(format))

			mojiWOFF2, err := Convert(source, filepath.Join(directory, "moji.woff2"), FormatWOFF2)
			if err != nil {
				t.Fatal(err)
			}
			fontToolsDecoded := filepath.Join(directory, "fonttools-decoded."+string(format))
			runFontTools(t, python, "decompress", mojiWOFF2.Output, fontToolsDecoded)
			assertDetectedFile(t, fontToolsDecoded, format)

			fontToolsWOFF2 := filepath.Join(directory, "fonttools.woff2")
			runFontTools(t, python, "compress", source, fontToolsWOFF2)
			mojiDecoded, err := Convert(fontToolsWOFF2, filepath.Join(directory, "moji-decoded."+string(format)), format)
			if err != nil {
				t.Fatal(err)
			}
			assertDetectedFile(t, mojiDecoded.Output, format)
		})
	}
}

func runFontTools(t *testing.T, python, operation, input, output string) {
	t.Helper()
	program := `
import sys
from fontTools.ttLib.woff2 import compress, decompress
{"compress": compress, "decompress": decompress}[sys.argv[1]](sys.argv[2], sys.argv[3])
`
	command := exec.Command(python, "-c", program, operation, input, output)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("FontTools %s: %v\n%s", operation, err, result)
	}
}

func assertDetectedFile(t *testing.T, path string, expected Format) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := Detect(content); err != nil || actual != expected {
		t.Fatalf("%s format = %q, %v; want %q", path, actual, err, expected)
	}
}
