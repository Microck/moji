package archivefont

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

type Limits struct {
	MaxEntries      int
	MaxMemberSize   int64
	MaxExpandedSize int64
}

type Member struct {
	Path   string
	Format string
	Size   int64
}

func DefaultLimits() Limits {
	return Limits{MaxEntries: 2_000, MaxMemberSize: 50 << 20, MaxExpandedSize: 250 << 20}
}

func Inspect(content []byte, format string, allowed map[string]bool, limits Limits) ([]Member, error) {
	if limits.MaxEntries <= 0 || limits.MaxMemberSize <= 0 || limits.MaxExpandedSize <= 0 {
		limits = DefaultLimits()
	}
	switch strings.ToLower(format) {
	case "zip":
		reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return nil, fmt.Errorf("invalid ZIP archive: %w", err)
		}
		if len(reader.File) > limits.MaxEntries {
			return nil, fmt.Errorf("archive has %d entries; limit is %d", len(reader.File), limits.MaxEntries)
		}
		members := make([]Member, 0)
		var expanded int64
		for _, file := range reader.File {
			if !safePath(file.Name) || file.FileInfo().IsDir() {
				continue
			}
			if file.UncompressedSize64 > uint64(limits.MaxMemberSize) {
				return nil, errors.New("archive exceeds expanded-size safety limits")
			}
			size := int64(file.UncompressedSize64)
			if size > limits.MaxExpandedSize-expanded {
				return nil, errors.New("archive exceeds expanded-size safety limits")
			}
			expanded += size
			if format := memberFormat(file.Name, allowed); format != "" {
				members = append(members, Member{Path: file.Name, Format: format, Size: size})
			}
		}
		return members, nil
	case "tar", "tgz", "tar.gz":
		return inspectTar(content, format, allowed, limits)
	default:
		return nil, fmt.Errorf("unsupported archive format %q", format)
	}
}

func Extract(content []byte, format, memberPath string, limits Limits) ([]byte, error) {
	allowed := map[string]bool{strings.TrimPrefix(strings.ToLower(filepath.Ext(memberPath)), "."): true}
	members, err := Inspect(content, format, allowed, limits)
	if err != nil {
		return nil, err
	}
	found := false
	for _, member := range members {
		if member.Path == memberPath {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("archive member %q is missing or unsafe", memberPath)
	}
	if strings.EqualFold(format, "zip") {
		reader, _ := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		for _, file := range reader.File {
			if file.Name == memberPath {
				opened, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer opened.Close()
				return readBounded(opened, limits.MaxMemberSize)
			}
		}
	}
	reader, closer, err := tarReader(content, format)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Name == memberPath {
			return readBounded(reader, limits.MaxMemberSize)
		}
	}
	return nil, fmt.Errorf("archive member %q was not found", memberPath)
}

func inspectTar(content []byte, format string, allowed map[string]bool, limits Limits) ([]Member, error) {
	reader, closer, err := tarReader(content, format)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}
	members := make([]Member, 0)
	entries := 0
	var expanded int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid TAR archive: %w", err)
		}
		entries++
		if entries > limits.MaxEntries || header.Size < 0 || header.Size > limits.MaxMemberSize || header.Size > limits.MaxExpandedSize-expanded {
			return nil, errors.New("archive exceeds entry or expanded-size safety limits")
		}
		expanded += header.Size
		if header.Typeflag == tar.TypeReg && safePath(header.Name) {
			if format := memberFormat(header.Name, allowed); format != "" {
				members = append(members, Member{Path: header.Name, Format: format, Size: header.Size})
			}
		}
	}
	return members, nil
}

func tarReader(content []byte, format string) (*tar.Reader, io.Closer, error) {
	reader := bytes.NewReader(content)
	if strings.EqualFold(format, "tgz") || strings.EqualFold(format, "tar.gz") {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid gzip archive: %w", err)
		}
		return tar.NewReader(gzipReader), gzipReader, nil
	}
	return tar.NewReader(reader), nil, nil
}

func safePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && !strings.HasPrefix(cleaned, "../")
}

func memberFormat(name string, allowed map[string]bool) string {
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if allowed[format] {
		return format
	}
	return ""
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, errors.New("archive member exceeds the size limit")
	}
	return content, nil
}
