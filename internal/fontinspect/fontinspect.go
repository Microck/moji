package fontinspect

import (
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/microck/moji/internal/fontconvert"
	"golang.org/x/image/font/sfnt"
)

// Result describes the Unicode characters a local font maps to glyphs. Scripts
// with no encoded characters are omitted so ordinary fonts produce concise
// output; absence from Scripts therefore means zero coverage.
type Result struct {
	Path              string             `json:"path"`
	Format            fontconvert.Format `json:"format"`
	Family            string             `json:"family,omitempty"`
	Subfamily         string             `json:"subfamily,omitempty"`
	Glyphs            int                `json:"glyphs"`
	UnicodeVersion    string             `json:"unicode_version"`
	EncodedCharacters int                `json:"encoded_characters"`
	Scripts           []ScriptCoverage   `json:"scripts"`
}

type ScriptCoverage struct {
	Name     string  `json:"name"`
	Encoded  int     `json:"encoded"`
	Assigned int     `json:"assigned"`
	Coverage float64 `json:"coverage"`
}

type UnsupportedError struct {
	Message string
}

func (failure UnsupportedError) Error() string { return failure.Message }

func IsUnsupported(err error) bool {
	var failure UnsupportedError
	return errors.As(err, &failure) || fontconvert.IsUnsupported(err)
}

func Inspect(path string) (Result, error) {
	if path == "" {
		return Result{}, errors.New("font input is required")
	}
	file, err := openInput(path)
	if err != nil {
		return Result{}, fmt.Errorf("inspect input %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("inspect input %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("input %s is not a regular file", path)
	}
	if info.Size() > fontconvert.MaxSize {
		return Result{}, fmt.Errorf("input is larger than the %d-byte inspection limit", fontconvert.MaxSize)
	}
	content, err := io.ReadAll(io.LimitReader(file, fontconvert.MaxSize+1))
	if err != nil {
		return Result{}, fmt.Errorf("read input %s: %w", path, err)
	}
	if int64(len(content)) > fontconvert.MaxSize {
		return Result{}, fmt.Errorf("input is larger than the %d-byte inspection limit", fontconvert.MaxSize)
	}
	if len(content) >= 4 && string(content[:4]) == "wOFF" {
		return Result{}, UnsupportedError{Message: "WOFF1 inspection is not supported; use a TTF, OTF, or WOFF2 font"}
	}
	desktop, format, err := fontconvert.DecodeSFNT(content)
	if err != nil {
		if legacyFormat := legacyExtension(path); legacyFormat != "" {
			return Result{}, UnsupportedError{
				Message: fmt.Sprintf("%s inspection is not supported; use a TTF, OTF, or WOFF2 font", legacyFormat),
			}
		}
		return Result{}, fmt.Errorf("inspect input font: %w", err)
	}

	// sfnt.Parse provides the cmap lookup used below. Its API keeps a reference
	// to desktop, so the bytes must remain alive until all coverage work ends.
	font, err := sfnt.Parse(desktop)
	if err != nil {
		return Result{}, fmt.Errorf("parse input font: %w", err)
	}
	family, err := fontName(font, sfnt.NameIDTypographicFamily, sfnt.NameIDFamily)
	if err != nil {
		return Result{}, err
	}
	subfamily, err := fontName(font, sfnt.NameIDTypographicSubfamily, sfnt.NameIDSubfamily)
	if err != nil {
		return Result{}, err
	}
	scripts, encodedCharacters, err := scriptCoverage(font)
	if err != nil {
		return Result{}, fmt.Errorf("read font character map: %w", err)
	}
	return Result{
		Path:              path,
		Format:            format,
		Family:            family,
		Subfamily:         subfamily,
		Glyphs:            font.NumGlyphs(),
		UnicodeVersion:    unicode.Version,
		EncodedCharacters: encodedCharacters,
		Scripts:           scripts,
	}, nil
}

func legacyExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".woff":
		return "WOFF1"
	case ".dfont":
		return "dfont"
	case ".pfb":
		return "PFB"
	case ".pfm":
		return "PFM"
	default:
		return ""
	}
}

func fontName(font *sfnt.Font, preferred, fallback sfnt.NameID) (string, error) {
	var buffer sfnt.Buffer
	name, err := font.Name(&buffer, preferred)
	if errors.Is(err, sfnt.ErrNotFound) || name == "" {
		name, err = font.Name(&buffer, fallback)
	}
	if errors.Is(err, sfnt.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read font name: %w", err)
	}
	return name, nil
}

func scriptCoverage(font *sfnt.Font) ([]ScriptCoverage, int, error) {
	names := make([]string, 0, len(unicode.Scripts))
	for name := range unicode.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build a compact derived lookup once, then scan the cmap once. This keeps
	// the top-level count exact even for mapped private-use or other characters
	// outside named script tables, without repeatedly searching every script.
	scriptByRune := make([]uint16, unicode.MaxRune+1)
	assigned := make([]int, len(names))
	for index, name := range names {
		visitRangeTable(unicode.Scripts[name], func(codepoint rune) {
			scriptByRune[codepoint] = uint16(index + 1)
			assigned[index]++
		})
	}

	var buffer sfnt.Buffer
	encoded := make([]int, len(names))
	encodedCharacters := 0
	for codepoint := rune(0); codepoint <= unicode.MaxRune; codepoint++ {
		if codepoint >= 0xD800 && codepoint <= 0xDFFF {
			continue
		}
		glyph, err := font.GlyphIndex(&buffer, codepoint)
		if err != nil {
			return nil, 0, err
		}
		if glyph == 0 {
			continue
		}
		encodedCharacters++
		if script := scriptByRune[codepoint]; script != 0 {
			encoded[script-1]++
		}
	}

	scripts := make([]ScriptCoverage, 0)
	for index, name := range names {
		if encoded[index] == 0 {
			continue
		}
		percentage := math.Round(float64(encoded[index])*10000/float64(assigned[index])) / 100
		scripts = append(scripts, ScriptCoverage{
			Name: name, Encoded: encoded[index], Assigned: assigned[index], Coverage: percentage,
		})
	}
	return scripts, encodedCharacters, nil
}

func visitRangeTable(table *unicode.RangeTable, visit func(rune)) {
	for _, item := range table.R16 {
		for codepoint := rune(item.Lo); codepoint <= rune(item.Hi); codepoint += rune(item.Stride) {
			visit(codepoint)
		}
	}
	for _, item := range table.R32 {
		for codepoint := rune(item.Lo); codepoint <= rune(item.Hi); codepoint += rune(item.Stride) {
			visit(codepoint)
		}
	}
}
