package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/microck/moji/internal/archivefont"
	"github.com/microck/moji/internal/provider"
)

const DefaultMaxSize int64 = 50 << 20

type Downloader struct {
	Client        *http.Client
	MaxSize       int64
	AllowInsecure bool
}

type File struct {
	Path     string
	SHA256   string
	Existing bool
}

type batchMove struct {
	from string
	to   string
}

// InvalidContentError marks a URL that returned bytes which cannot be the
// advertised font. Callers may remember this failure without treating network
// or local filesystem errors as permanent URL failures.
type InvalidContentError struct {
	URL           string
	ArchiveMember string
	Err           error
}

func (failure InvalidContentError) Error() string { return failure.Err.Error() }
func (failure InvalidContentError) Unwrap() error { return failure.Err }

func IsInvalidContent(err error) bool {
	var failure InvalidContentError
	return errors.As(err, &failure)
}

func InvalidContentKey(err error) string {
	var failure InvalidContentError
	if errors.As(err, &failure) {
		if failure.ArchiveMember != "" {
			return failure.URL + "\x00" + failure.ArchiveMember
		}
		return failure.URL
	}
	return ""
}

func (d Downloader) Download(ctx context.Context, result provider.Result, destination string) (File, error) {
	parsed, err := url.Parse(result.URL)
	if err != nil {
		return File{}, fmt.Errorf("Moji couldn't use the download link from %s: %w. Try another result", result.Source, err)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && d.AllowInsecure) {
		return File{}, errors.New("the download uses insecure HTTP, so Moji blocked it before saving a file. Choose another result or use --allow-insecure only if you trust this source")
	}
	client := d.Client
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	originalRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" && !(request.URL.Scheme == "http" && d.AllowInsecure) {
			return errors.New("the download redirected to insecure HTTP, so Moji stopped before saving a file. Choose another result or use --allow-insecure only if you trust this source")
		}
		if len(via) >= 5 {
			return errors.New("the download redirected more than 5 times, so Moji stopped before saving a file. Try another source")
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	client = &clientCopy
	limit := d.MaxSize
	if limit <= 0 {
		limit = DefaultMaxSize
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, result.URL, nil)
	if err != nil {
		return File{}, err
	}
	response, err := client.Do(req)
	if err != nil {
		return File{}, fmt.Errorf("Moji couldn't connect to %s, so no file was saved: %w. Check your connection or try another source", result.Source, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return File{}, fmt.Errorf("%s couldn't provide the font (HTTP %d), so no file was saved. Try another result", result.Source, response.StatusCode)
	}
	if response.ContentLength > limit {
		return File{}, fmt.Errorf("the font is larger than the %d-byte safety limit, so no file was saved. Try another result", limit)
	}
	contentReader := io.Reader(response.Body)
	if result.ArchiveMember != "" {
		archive, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if readErr != nil {
			return File{}, fmt.Errorf("the archive download stopped before it completed: %w. No file was saved", readErr)
		}
		if int64(len(archive)) > limit {
			return File{}, fmt.Errorf("the archive is larger than the %d-byte safety limit, so no file was saved", limit)
		}
		member, extractErr := archivefont.Extract(archive, result.ArchiveFormat, result.ArchiveMember, archivefont.DefaultLimits())
		if extractErr != nil {
			return File{}, InvalidContentError{URL: result.URL, ArchiveMember: result.ArchiveMember, Err: fmt.Errorf("Moji couldn't safely extract %s: %w. No file was saved", result.ArchiveMember, extractErr)}
		}
		contentReader = bytes.NewReader(member)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return File{}, fmt.Errorf("Moji couldn't create the download directory %s: %w. Check the directory permissions, then try again", destination, err)
	}
	filename := filepath.Base(result.Filename)
	if filename == "." || filename == "" {
		return File{}, errors.New("the result has no usable filename, so no file was saved. Try another result")
	}
	temporary, err := os.CreateTemp(destination, ".moji-*.tmp")
	if err != nil {
		return File{}, fmt.Errorf("Moji couldn't create a temporary file in %s: %w. No font was saved; check the directory permissions", destination, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(contentReader, limit+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return File{}, fmt.Errorf("the download stopped before it completed: %w. No font was saved; check your connection and try again", copyErr)
	}
	if closeErr != nil {
		return File{}, fmt.Errorf("Moji couldn't finish the temporary download: %w. No font was saved; check the directory permissions", closeErr)
	}
	if written > limit {
		return File{}, fmt.Errorf("the font is larger than the %d-byte safety limit, so no file was saved. Try another result", limit)
	}
	bytes, err := os.ReadFile(temporaryPath)
	if err != nil {
		return File{}, fmt.Errorf("Moji couldn't validate the temporary download: %w. No font was saved; try again", err)
	}
	if err := ValidateMagic(result.Format, bytes); err != nil {
		return File{}, InvalidContentError{URL: result.URL, ArchiveMember: result.ArchiveMember, Err: err}
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	finalPath := filepath.Join(destination, filename)
	if duplicatePath, found, err := findDuplicate(destination, digest); err != nil {
		return File{}, fmt.Errorf("Moji couldn't inspect existing files in %s: %w. No new file was saved; check the directory permissions", destination, err)
	} else if found {
		return File{Path: duplicatePath, SHA256: digest, Existing: true}, nil
	}
	if existing, err := os.ReadFile(finalPath); err == nil {
		existingHash := sha256.Sum256(existing)
		if hex.EncodeToString(existingHash[:]) == digest {
			return File{Path: finalPath, SHA256: digest, Existing: true}, nil
		}
		return File{}, fmt.Errorf("%s already contains a different file. Move or rename it, then try again", finalPath)
	}
	if err := moveNoReplace(temporaryPath, finalPath); err != nil {
		if existing, readErr := os.ReadFile(finalPath); readErr == nil {
			existingHash := sha256.Sum256(existing)
			if hex.EncodeToString(existingHash[:]) == digest {
				return File{Path: finalPath, SHA256: digest, Existing: true}, nil
			}
			return File{}, fmt.Errorf("%s was created by another process with different content. Moji did not overwrite it", finalPath)
		}
		return File{}, fmt.Errorf("Moji couldn't move the validated font to %s: %w. No completed font was saved; check the directory permissions", finalPath, err)
	}
	return File{Path: finalPath, SHA256: digest}, nil
}

// DownloadBatch validates every file in an isolated staging directory before
// exposing any of them in destination. This keeps family downloads coherent:
// one bad member cannot leave a partially downloaded family behind.
func (d Downloader) DownloadBatch(ctx context.Context, results []provider.Result, destination string) ([]File, error) {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, fmt.Errorf("Moji couldn't create the download directory %s: %w. Check the directory permissions, then try again", destination, err)
	}
	staging, err := os.MkdirTemp(destination, ".moji-family-*")
	if err != nil {
		return nil, fmt.Errorf("Moji couldn't create a family staging directory in %s: %w. No font was saved", destination, err)
	}
	defer os.RemoveAll(staging)

	staged := make([]File, 0, len(results))
	stagedPaths := make(map[string]bool, len(results))
	for _, result := range results {
		file, downloadErr := d.Download(ctx, result, staging)
		if downloadErr != nil {
			return nil, downloadErr
		}
		if !stagedPaths[file.Path] {
			stagedPaths[file.Path] = true
			staged = append(staged, file)
		}
	}
	release, err := lockDownloadDirectory(destination)
	if err != nil {
		return nil, fmt.Errorf("Moji couldn't lock %s for a family download: %w. No family files were saved", destination, err)
	}
	defer release()

	files := make([]File, len(staged))
	moves := make([]batchMove, 0, len(staged))
	for index, file := range staged {
		finalPath := filepath.Join(destination, filepath.Base(file.Path))
		if duplicatePath, found, findErr := findDuplicate(destination, file.SHA256); findErr != nil {
			return nil, fmt.Errorf("Moji couldn't inspect existing files in %s: %w. No family files were saved", destination, findErr)
		} else if found {
			files[index] = File{Path: duplicatePath, SHA256: file.SHA256, Existing: true}
			continue
		}
		if existing, readErr := os.ReadFile(finalPath); readErr == nil {
			hash := sha256.Sum256(existing)
			if hex.EncodeToString(hash[:]) != file.SHA256 {
				return nil, fmt.Errorf("%s already contains a different file. No family files were saved; move or rename it, then try again", finalPath)
			}
			files[index] = File{Path: finalPath, SHA256: file.SHA256, Existing: true}
			continue
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("Moji couldn't inspect %s: %w. No family files were saved", finalPath, readErr)
		}
		files[index] = File{Path: finalPath, SHA256: file.SHA256}
		moves = append(moves, batchMove{from: file.Path, to: finalPath})
	}

	if err := commitBatchMoves(moves); err != nil {
		return nil, err
	}
	return files, nil
}

func commitBatchMoves(moves []batchMove) error {
	type committedMove struct {
		batchMove
		identity os.FileInfo
	}
	committed := make([]committedMove, 0, len(moves))
	for _, operation := range moves {
		if err := moveNoReplace(operation.from, operation.to); err != nil {
			rollbackFailures := make([]error, 0)
			for index := len(committed) - 1; index >= 0; index-- {
				current, statErr := os.Stat(committed[index].to)
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				if statErr != nil || !os.SameFile(committed[index].identity, current) {
					rollbackFailures = append(rollbackFailures, fmt.Errorf("%s changed before rollback", committed[index].to))
					continue
				}
				if removeErr := os.Remove(committed[index].to); removeErr != nil {
					rollbackFailures = append(rollbackFailures, fmt.Errorf("remove %s: %w", committed[index].to, removeErr))
				}
			}
			if len(rollbackFailures) > 0 {
				return fmt.Errorf("Moji couldn't finish the family download at %s without overwriting a concurrent file: %w. Rollback was incomplete: %v", operation.to, err, errors.Join(rollbackFailures...))
			}
			return fmt.Errorf("Moji couldn't finish the family download at %s without overwriting a concurrent file: %w. Earlier family moves were rolled back", operation.to, err)
		}
		identity, statErr := os.Stat(operation.to)
		if statErr != nil {
			return fmt.Errorf("Moji couldn't verify the committed family file %s: %w", operation.to, statErr)
		}
		committed = append(committed, committedMove{batchMove: operation, identity: identity})
	}
	return nil
}

func findDuplicate(directory, digest string) (string, bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".tmp") || strings.HasPrefix(entry.Name(), ".moji-") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return "", false, err
		}
		hash := sha256.Sum256(content)
		if hex.EncodeToString(hash[:]) == digest {
			return path, true, nil
		}
	}
	return "", false, nil
}

func ValidateMagic(format string, content []byte) error {
	if len(content) < 4 {
		return errors.New("the downloaded content is too short to be a font. No file was saved; try another source")
	}
	magic := string(content[:4])
	valid := map[string][]string{"otf": {"OTTO"}, "ttf": {"\x00\x01\x00\x00", "OTTO"}, "woff": {"wOFF"}, "woff2": {"wOF2"}}
	format = strings.ToLower(format)
	switch format {
	case "pfb":
		if validatePFB(content) {
			return nil
		}
		return errors.New("the downloaded content is not a valid pfb Type 1 font. No file was saved; try another source")
	case "pfm":
		if len(content) >= 117 && binary.LittleEndian.Uint16(content[:2]) == 0x0100 && int(binary.LittleEndian.Uint32(content[2:6])) == len(content) {
			return nil
		}
		return errors.New("the downloaded content is not a valid pfm metrics file. No file was saved; try another source")
	case "dfont":
		if validateDFont(content) {
			return nil
		}
		return errors.New("the downloaded content is not a valid dfont resource font. No file was saved; try another source")
	}
	for _, expected := range valid[format] {
		if magic == expected {
			return nil
		}
	}
	if _, supported := valid[format]; !supported {
		return fmt.Errorf("Moji can't validate the %q font format. No file was saved; choose otf, ttf, woff, woff2, dfont, pfb, or pfm", format)
	}
	return fmt.Errorf("the downloaded content is not a valid %s font. No file was saved; try another source", format)
}

func validatePFB(content []byte) bool {
	for offset := 0; offset+2 <= len(content); {
		if content[offset] != 0x80 {
			return false
		}
		kind := content[offset+1]
		if kind == 0x03 {
			return offset+2 == len(content)
		}
		if (kind != 0x01 && kind != 0x02) || offset+6 > len(content) {
			return false
		}
		length := int(binary.LittleEndian.Uint32(content[offset+2 : offset+6]))
		if length <= 0 || offset+6+length > len(content) {
			return false
		}
		offset += 6 + length
	}
	return false
}

func validateDFont(content []byte) bool {
	if len(content) < 16 {
		return false
	}
	dataOffset := int(binary.BigEndian.Uint32(content[0:4]))
	mapOffset := int(binary.BigEndian.Uint32(content[4:8]))
	dataLength := int(binary.BigEndian.Uint32(content[8:12]))
	mapLength := int(binary.BigEndian.Uint32(content[12:16]))
	if dataOffset < 16 || mapOffset < 16 || dataLength <= 0 || mapLength <= 0 ||
		dataOffset > len(content)-dataLength || mapOffset > len(content)-mapLength {
		return false
	}
	return bytes.Contains(content[mapOffset:mapOffset+mapLength], []byte("sfnt"))
}
