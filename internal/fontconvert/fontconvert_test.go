package fontconvert

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRecognizesSupportedContainersFromContent(t *testing.T) {
	t.Parallel()
	tests := map[string]Format{
		"test-ttf.base64":   FormatTTF,
		"test-otf.base64":   FormatOTF,
		"test-woff2.base64": FormatWOFF2,
	}
	for fixture, expected := range tests {
		fixture, expected := fixture, expected
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			if actual, err := Detect(readFixture(t, fixture)); err != nil || actual != expected {
				t.Fatalf("Detect() = %q, %v; want %q", actual, err, expected)
			}
		})
	}
}

func TestConvertRoundTripsTrueTypeAndCFFFontsThroughWOFF2(t *testing.T) {
	t.Parallel()
	for fixture, sourceFormat := range map[string]Format{
		"test-ttf.base64": FormatTTF,
		"test-otf.base64": FormatOTF,
	} {
		fixture, sourceFormat := fixture, sourceFormat
		t.Run(string(sourceFormat), func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			input := writeFixture(t, directory, fixture, "font.input")

			compressed, err := Convert(input, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if compressed.SourceFormat != sourceFormat || compressed.TargetFormat != FormatWOFF2 || filepath.Ext(compressed.Output) != ".woff2" {
				t.Fatalf("compressed = %#v", compressed)
			}
			assertFileIdentity(t, compressed)

			restoredPath := filepath.Join(directory, "restored."+string(sourceFormat))
			restored, err := Convert(compressed.Output, restoredPath, sourceFormat)
			if err != nil {
				t.Fatal(err)
			}
			if restored.SourceFormat != FormatWOFF2 || restored.TargetFormat != sourceFormat || restored.Output != restoredPath {
				t.Fatalf("restored = %#v", restored)
			}
			content, readErr := os.ReadFile(restored.Output)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if actual, detectErr := Detect(content); detectErr != nil || actual != sourceFormat {
				t.Fatalf("restored format = %q, %v; want %q", actual, detectErr, sourceFormat)
			}
			assertFileIdentity(t, restored)
		})
	}
}

func TestConvertDecodesFontToolsWOFF2Fixture(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := writeFixture(t, directory, "test-woff2.base64", "font.woff2")
	converted, err := Convert(input, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if converted.TargetFormat != FormatTTF || filepath.Ext(converted.Output) != ".ttf" {
		t.Fatalf("converted = %#v", converted)
	}
	content, err := os.ReadFile(converted.Output)
	if err != nil || !bytes.HasPrefix(content, []byte{0, 1, 0, 0}) {
		t.Fatalf("decoded header = %x, err = %v", content[:min(4, len(content))], err)
	}
}

func TestConvertRejectsOutlineChangesAndFlavorMismatches(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	ttf := writeFixture(t, directory, "test-ttf.base64", "font.ttf")
	if _, err := Convert(ttf, "", FormatTTF); err == nil || !IsUnsupported(err) {
		t.Fatalf("same-format error = %v", err)
	}
	if _, err := Convert(ttf, "", FormatOTF); err == nil || !IsUnsupported(err) {
		t.Fatalf("TTF-to-OTF error = %v", err)
	}
	woff2 := writeFixture(t, directory, "test-woff2.base64", "font.woff2")
	if _, err := Convert(woff2, filepath.Join(directory, "font.otf"), FormatOTF); err == nil || !IsUnsupported(err) {
		t.Fatalf("TrueType WOFF2-to-OTF error = %v", err)
	}
}

func TestConvertRejectsOversizedInputBeforeReadingIt(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := filepath.Join(directory, "oversized.ttf")
	file, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxSize + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(input, "", ""); err == nil || !strings.Contains(err.Error(), "conversion limit") {
		t.Fatalf("oversized input error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "oversized.woff2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized input left output: %v", err)
	}
}

func TestConvertRejectsMalformedAndCollectionInputWithoutResidue(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		content     []byte
		unsupported bool
	}{
		"truncated":  {content: []byte("wOF2")},
		"collection": {content: []byte("ttcf-not-supported"), unsupported: true},
		"unknown":    {content: []byte("not a font")},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			input := filepath.Join(directory, "input")
			output := filepath.Join(directory, "output.woff2")
			if err := os.WriteFile(input, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Convert(input, output, FormatWOFF2); err == nil || IsUnsupported(err) != test.unsupported {
				t.Fatalf("Convert() error = %v", err)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed conversion left output: %v", err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 || entries[0].Name() != "input" {
				t.Fatalf("failed conversion left residue: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestConvertNeverReplacesExistingDestination(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := writeFixture(t, directory, "test-ttf.base64", "font.ttf")
	output := filepath.Join(directory, "font.woff2")
	if err := os.WriteFile(output, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(input, output, FormatWOFF2); err == nil {
		t.Fatal("Convert() replaced an existing destination")
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "keep me" {
		t.Fatalf("destination = %q, %v", content, err)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil || len(entries) != 2 {
		t.Fatalf("collision left residue: entries=%v err=%v", entries, readErr)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func writeFixture(t *testing.T, directory, fixture, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, readFixture(t, fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileIdentity(t *testing.T, converted Result) {
	t.Helper()
	content, err := os.ReadFile(converted.Output)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if converted.Size != int64(len(content)) || converted.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("identity = size %d hash %q; file = size %d hash %q", converted.Size, converted.SHA256, len(content), hex.EncodeToString(digest[:]))
	}
}
