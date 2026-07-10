package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, limit+1))
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
		return File{}, err
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
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return File{}, fmt.Errorf("Moji couldn't move the validated font to %s: %w. No completed font was saved; check the directory permissions", finalPath, err)
	}
	return File{Path: finalPath, SHA256: digest}, nil
}

func findDuplicate(directory, digest string) (string, bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".tmp") {
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
	for _, expected := range valid[strings.ToLower(format)] {
		if magic == expected {
			return nil
		}
	}
	if _, supported := valid[strings.ToLower(format)]; !supported {
		return fmt.Errorf("Moji can't validate the %q font format. No file was saved; choose otf, ttf, woff, or woff2", format)
	}
	return fmt.Errorf("the downloaded content is not a valid %s font. No file was saved; try another source", format)
}
