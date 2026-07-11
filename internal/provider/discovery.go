package provider

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microck/moji/internal/archivefont"
	"github.com/microck/moji/internal/safehttp"
)

const maxDiscoveryContainerSize int64 = 50 << 20

const maxDiscoveryStylesheetSize int64 = 2 << 20

var cssSource = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")\s]+)['"]?\s*\)\s*(?:format\(\s*['"]?([a-z0-9-]+)['"]?\s*\))?`)

// privateDiscoveryContextKey lets package tests reach local TLS servers. No
// production caller can opt out of the public-address check through the API.
type privateDiscoveryContextKey struct{}

func resolveDiscoveredURL(ctx context.Context, client *http.Client, rawURL, query string, allowed map[string]bool) ([]Result, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, nil
	}
	if strings.EqualFold(parsed.Host, "github.com") {
		if converted := githubRawURL(parsed); converted != nil {
			parsed = converted
			rawURL = converted.String()
		}
	}
	filename := filepath.Base(parsed.Path)
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	if allowed[format] {
		return []Result{{
			Name: query, Filename: filename, Format: format, Source: parsed.Host,
			URL: rawURL, Trusted: false, License: "unknown", FamilyGroup: directFamilyGroup(parsed),
		}}, nil
	}
	if format == "css" {
		content, fetchErr := fetchDiscoveryContent(ctx, client, rawURL, maxDiscoveryStylesheetSize)
		if fetchErr != nil {
			return nil, fetchErr
		}
		results := make([]Result, 0)
		for _, match := range cssSource.FindAllSubmatch(content, -1) {
			reference, parseErr := url.Parse(string(match[1]))
			if parseErr != nil {
				continue
			}
			resolved := parsed.ResolveReference(reference)
			fontFormat := cssFontFormat(resolved.Path, string(match[2]))
			if resolved.Scheme == "https" && resolved.Host != "" && allowed[fontFormat] {
				results = append(results, Result{
					Name: query, Filename: discoveredFilename(resolved.Path, query, fontFormat), Format: fontFormat,
					Source: resolved.Host, URL: resolved.String(), Trusted: false, License: "unknown", FamilyGroup: discoveredFamilyGroup("css", rawURL),
				})
			}
		}
		return results, nil
	}
	archiveFormat := discoveredArchiveFormat(parsed.Path)
	var content []byte
	if archiveFormat == "" {
		content, _, err = fetchDiscoveryContentWithType(ctx, client, rawURL, maxDiscoveryContainerSize, true)
		if err != nil {
			return nil, err
		}
		archiveFormat = sniffArchiveFormat(content)
		if archiveFormat == "" {
			return nil, nil
		}
	} else {
		content, _, err = fetchDiscoveryContentWithType(ctx, client, rawURL, maxDiscoveryContainerSize, true)
		if err != nil {
			return nil, err
		}
		if content == nil {
			return nil, nil
		}
	}
	members, err := archivefont.Inspect(content, archiveFormat, allowed, archivefont.DefaultLimits())
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(members))
	for _, member := range members {
		results = append(results, Result{
			Name: query, Filename: filepath.Base(member.Path), Format: member.Format,
			SizeBytes: member.Size, Source: parsed.Host, URL: rawURL,
			Trusted: false, License: "unknown", ArchiveFormat: archiveFormat, ArchiveMember: member.Path, FamilyGroup: discoveredFamilyGroup("archive", rawURL),
		})
	}
	return results, nil
}

func directFamilyGroup(parsed *url.URL) string {
	if strings.EqualFold(parsed.Hostname(), "raw.githubusercontent.com") {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 {
			return "github.com/" + parts[0] + "/" + parts[1]
		}
	}
	return discoveredFamilyGroup("url", parsed.String())
}

func discoveredFamilyGroup(kind, value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s:%x", kind, digest)
}

func discoveredFilename(pathValue, query, format string) string {
	filename := filepath.Base(pathValue)
	if filename == "." || filename == "/" || filename == "" {
		filename = strings.Join(strings.Fields(query), "-")
	}
	if filepath.Ext(filename) == "" {
		filename += "." + format
	}
	return filename
}

func fetchDiscoveryContent(ctx context.Context, client *http.Client, rawURL string, maximum int64) ([]byte, error) {
	content, _, err := fetchDiscoveryContentWithType(ctx, client, rawURL, maximum, false)
	return content, err
}

func fetchDiscoveryContentWithType(ctx context.Context, client *http.Client, rawURL string, maximum int64, requireBinary bool) ([]byte, string, error) {
	allowPrivate, _ := ctx.Value(privateDiscoveryContextKey{}).(bool)
	clientCopy := safehttp.Constrain(client, allowPrivate)
	originalRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("discovery redirected to an insecure URL")
		}
		if len(via) >= 5 {
			return errors.New("discovery redirected more than 5 times")
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := clientCopy.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("%w: discovery request: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", nil
	}
	if response.ContentLength > maximum {
		return nil, "", errors.New("discovery response exceeds the size limit")
	}
	contentType := response.Header.Get("Content-Type")
	if requireBinary && pageContentType(contentType) {
		return nil, contentType, nil
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(content)) > maximum {
		return nil, "", errors.New("discovery response exceeds the size limit")
	}
	return content, contentType, nil
}

func pageContentType(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/ld+json", "application/xhtml+xml", "application/xml":
		return true
	default:
		return false
	}
}

func sniffArchiveFormat(content []byte) string {
	if len(content) >= 4 && content[0] == 'P' && content[1] == 'K' &&
		((content[2] == 3 && content[3] == 4) || (content[2] == 5 && content[3] == 6) || (content[2] == 7 && content[3] == 8)) {
		return "zip"
	}
	if len(content) >= 2 && content[0] == 0x1f && content[1] == 0x8b {
		return "tgz"
	}
	if len(content) >= 262 && string(content[257:262]) == "ustar" {
		return "tar"
	}
	return ""
}

func cssFontFormat(pathValue, declared string) string {
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(pathValue)), ".")
	if format != "" {
		return format
	}
	switch strings.ToLower(declared) {
	case "opentype":
		return "otf"
	case "truetype":
		return "ttf"
	default:
		return strings.ToLower(declared)
	}
}

func discoveredArchiveFormat(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tgz"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	default:
		return ""
	}
}
