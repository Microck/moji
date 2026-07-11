package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveLoadAndPermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".moji", "config.yaml")
	want := Default()
	want.DownloadDir = "/tmp/fonts"
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded config differs:\n%#v\n%#v", got, want)
	}
}

func TestParseFormats(t *testing.T) {
	t.Parallel()
	got, err := ParseFormats("OTF, ttf,otf")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"otf", "ttf"}) {
		t.Fatalf("formats = %#v", got)
	}
	if _, err := ParseFormats("exe"); err == nil {
		t.Fatal("expected unsupported format error")
	}
	legacy, err := ParseFormats("dfont,pfb,pfm")
	if err != nil || !reflect.DeepEqual(legacy, []string{"dfont", "pfb", "pfm"}) {
		t.Fatalf("legacy formats = %#v, err=%v", legacy, err)
	}
}

func TestEnvironmentTokenPrecedesConfig(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "environment")
	config := Default()
	config.GitHubToken = "file"
	if config.Token() != "environment" {
		t.Fatalf("token = %q", config.Token())
	}
}

func TestMalformedConfigExplainsRecovery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("providers: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "Fix the YAML or move the file aside") {
		t.Fatalf("error = %v", err)
	}
}
