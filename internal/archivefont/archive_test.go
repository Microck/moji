package archivefont

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestInspectAndExtractZIPFonts(t *testing.T) {
	t.Parallel()
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	font, _ := writer.Create("Family/Example-Regular.otf")
	font.Write([]byte("OTTOfont"))
	unsafe, _ := writer.Create("../escape.ttf")
	unsafe.Write([]byte("ignored"))
	writer.Close()
	allowed := map[string]bool{"otf": true, "ttf": true}
	members, err := Inspect(content.Bytes(), "zip", allowed, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Path != "Family/Example-Regular.otf" {
		t.Fatalf("members = %#v", members)
	}
	extracted, err := Extract(content.Bytes(), "zip", members[0].Path, DefaultLimits())
	if err != nil || string(extracted) != "OTTOfont" {
		t.Fatalf("content=%q err=%v", extracted, err)
	}
}

func TestInspectAndExtractTarFonts(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"tar", "tgz"} {
		format := format
		t.Run(format, func(t *testing.T) {
			var content bytes.Buffer
			var destination io.Writer = &content
			var gzipWriter *gzip.Writer
			if format == "tgz" {
				gzipWriter = gzip.NewWriter(&content)
				destination = gzipWriter
			}
			writer := tar.NewWriter(destination)
			fontContent := []byte("OTTOfont")
			if err := writer.WriteHeader(&tar.Header{Name: "Family/Example-Regular.otf", Mode: 0o600, Size: int64(len(fontContent))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(fontContent); err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteHeader(&tar.Header{Name: "../escape.ttf", Mode: 0o600, Size: 0}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if gzipWriter != nil {
				if err := gzipWriter.Close(); err != nil {
					t.Fatal(err)
				}
			}
			members, err := Inspect(content.Bytes(), format, map[string]bool{"otf": true, "ttf": true}, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if len(members) != 1 || members[0].Path != "Family/Example-Regular.otf" {
				t.Fatalf("members = %#v", members)
			}
			extracted, err := Extract(content.Bytes(), format, members[0].Path, DefaultLimits())
			if err != nil || !bytes.Equal(extracted, fontContent) {
				t.Fatalf("content=%q err=%v", extracted, err)
			}
		})
	}
}

func TestInspectRejectsExpandedArchiveLimit(t *testing.T) {
	t.Parallel()
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	font, _ := writer.Create("Example.ttf")
	font.Write(make([]byte, 32))
	writer.Close()
	_, err := Inspect(content.Bytes(), "zip", map[string]bool{"ttf": true}, Limits{MaxEntries: 5, MaxMemberSize: 8, MaxExpandedSize: 16})
	if err == nil {
		t.Fatal("expected expanded-size rejection")
	}
}
