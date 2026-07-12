package fontconvert

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/microck/moji/internal/filecommit"
	"github.com/pgaskin/go-woff2"
)

const MaxSize int64 = 50 << 20

type Format string

const (
	FormatTTF   Format = "ttf"
	FormatOTF   Format = "otf"
	FormatWOFF2 Format = "woff2"
)

type Result struct {
	Input        string `json:"input"`
	Output       string `json:"output"`
	SourceFormat Format `json:"source_format"`
	TargetFormat Format `json:"target_format"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

// UnsupportedError distinguishes a recognized request that Moji intentionally
// does not implement from malformed font content or an operational failure.
type UnsupportedError struct {
	Message string
}

func (failure UnsupportedError) Error() string { return failure.Message }

func IsUnsupported(err error) bool {
	var failure UnsupportedError
	return errors.As(err, &failure)
}

func ParseFormat(value string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(value))) {
	case FormatTTF:
		return FormatTTF, nil
	case FormatOTF:
		return FormatOTF, nil
	case FormatWOFF2:
		return FormatWOFF2, nil
	default:
		return "", UnsupportedError{Message: fmt.Sprintf("unsupported conversion target %q; choose ttf, otf, or woff2", value)}
	}
}

// Detect identifies supported font containers by their binary headers. File
// extensions are deliberately ignored because downloaded web fonts are often
// mislabeled or extensionless.
func Detect(content []byte) (Format, error) {
	if len(content) < 4 {
		return "", errors.New("font content is too short to identify")
	}
	switch string(content[:4]) {
	case "\x00\x01\x00\x00", "true":
		return FormatTTF, nil
	case "OTTO":
		return FormatOTF, nil
	case "wOF2":
		if _, err := woff2DesktopFormat(content); err != nil {
			return "", err
		}
		return FormatWOFF2, nil
	case "ttcf":
		return "", UnsupportedError{Message: "font collections are not supported; extract one TTF or OTF font before converting"}
	case "wOFF":
		return "", UnsupportedError{Message: "WOFF1 conversion is not supported; use a TTF, OTF, or WOFF2 font"}
	default:
		return "", errors.New("content is not a supported TTF, OTF, or WOFF2 font")
	}
}

func Convert(inputPath, outputPath string, requested Format) (Result, error) {
	if strings.TrimSpace(inputPath) == "" {
		return Result{}, errors.New("input path is required")
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		return Result{}, fmt.Errorf("inspect input %s: %w", inputPath, err)
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("input %s is not a regular file", inputPath)
	}
	if info.Size() > MaxSize {
		return Result{}, fmt.Errorf("input is larger than the %d-byte conversion limit", MaxSize)
	}
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return Result{}, fmt.Errorf("read input %s: %w", inputPath, err)
	}
	if int64(len(content)) > MaxSize {
		return Result{}, fmt.Errorf("input is larger than the %d-byte conversion limit", MaxSize)
	}
	source, err := Detect(content)
	if err != nil {
		return Result{}, fmt.Errorf("inspect input font: %w", err)
	}
	target, err := resolveTarget(source, requested, content)
	if err != nil {
		return Result{}, err
	}
	if outputPath == "" {
		outputPath = defaultOutputPath(inputPath, target)
	}
	if samePath(inputPath, outputPath) {
		return Result{}, errors.New("input and output paths must be different")
	}

	converted, err := convertBytes(content, target)
	if err != nil {
		return Result{}, fmt.Errorf("convert %s to %s: %w", source, target, err)
	}
	if int64(len(converted)) > MaxSize {
		return Result{}, fmt.Errorf("converted font is larger than the %d-byte conversion limit", MaxSize)
	}
	if err := commitOutput(outputPath, converted, target); err != nil {
		return Result{}, err
	}
	digest := sha256.Sum256(converted)
	return Result{
		Input:        inputPath,
		Output:       outputPath,
		SourceFormat: source,
		TargetFormat: target,
		Size:         int64(len(converted)),
		SHA256:       hex.EncodeToString(digest[:]),
	}, nil
}

func resolveTarget(source, requested Format, content []byte) (Format, error) {
	inferred := FormatWOFF2
	if source == FormatWOFF2 {
		var err error
		inferred, err = woff2DesktopFormat(content)
		if err != nil {
			return "", err
		}
	}
	if requested == "" {
		return inferred, nil
	}
	if requested != FormatTTF && requested != FormatOTF && requested != FormatWOFF2 {
		return "", UnsupportedError{Message: fmt.Sprintf("unsupported conversion target %q; choose ttf, otf, or woff2", requested)}
	}
	if requested == source {
		return "", UnsupportedError{Message: fmt.Sprintf("%s input is already %s", source, requested)}
	}
	if source == FormatWOFF2 && requested != inferred {
		return "", UnsupportedError{Message: fmt.Sprintf("this WOFF2 font contains %s outlines and can only be restored to %s", inferred, inferred)}
	}
	if source != FormatWOFF2 && requested != FormatWOFF2 {
		return "", UnsupportedError{Message: fmt.Sprintf("Moji can't convert %s to %s because that would require changing glyph outlines", source, requested)}
	}
	return requested, nil
}

func woff2DesktopFormat(content []byte) (Format, error) {
	if len(content) < 12 {
		return "", errors.New("WOFF2 content is too short to contain a valid header")
	}
	switch string(content[4:8]) {
	case "\x00\x01\x00\x00", "true":
		return FormatTTF, nil
	case "OTTO":
		return FormatOTF, nil
	default:
		return "", fmt.Errorf("WOFF2 contains unsupported sfntVersion %x", content[4:8])
	}
}

func convertBytes(content []byte, target Format) ([]byte, error) {
	if target == FormatWOFF2 {
		return woff2.Encode(content, &woff2.Params{BrotliQuality: 11, AllowTransforms: true})
	}
	decodedSize, err := woff2.DecodeLength(content)
	if err != nil {
		return nil, err
	}
	if int64(decodedSize) > MaxSize {
		return nil, fmt.Errorf("decoded font is larger than the %d-byte conversion limit", MaxSize)
	}
	decoded, err := woff2.DecodeBytes(content)
	if err != nil {
		return nil, err
	}
	actual, err := Detect(decoded)
	if err != nil {
		return nil, fmt.Errorf("validate decoded font: %w", err)
	}
	if actual != target {
		return nil, fmt.Errorf("decoded font is %s, expected %s", actual, target)
	}
	return decoded, nil
}

func commitOutput(outputPath string, content []byte, expected Format) error {
	directory := filepath.Dir(outputPath)
	temporary, err := os.CreateTemp(directory, ".moji-convert-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary conversion output in %s: %w", directory, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary conversion output permissions: %w", err)
	}
	_, writeErr := io.Copy(temporary, bytes.NewReader(content))
	closeErr := temporary.Close()
	if writeErr != nil {
		return fmt.Errorf("write temporary conversion output: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary conversion output: %w", closeErr)
	}
	written, err := os.ReadFile(temporaryPath)
	if err != nil {
		return fmt.Errorf("validate temporary conversion output: %w", err)
	}
	actual, err := Detect(written)
	if err != nil {
		return fmt.Errorf("validate temporary conversion output: %w", err)
	}
	if actual != expected {
		return fmt.Errorf("validate temporary conversion output: detected %s, expected %s", actual, expected)
	}
	if err := filecommit.MoveNoReplace(temporaryPath, outputPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; move or rename it, then try again", outputPath)
		}
		return fmt.Errorf("commit converted font to %s: %w", outputPath, err)
	}
	return nil
}

func defaultOutputPath(inputPath string, target Format) string {
	extension := filepath.Ext(inputPath)
	base := strings.TrimSuffix(inputPath, extension)
	if base == "" {
		base = inputPath
	}
	return base + "." + string(target)
}

func samePath(first, second string) bool {
	firstAbsolute, firstErr := filepath.Abs(first)
	secondAbsolute, secondErr := filepath.Abs(second)
	return firstErr == nil && secondErr == nil && filepath.Clean(firstAbsolute) == filepath.Clean(secondAbsolute)
}
