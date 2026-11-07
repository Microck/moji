//go:build unix

package fontinstall

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInstallRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, atSource := range []bool{false, true} {
		name := "destination"
		if atSource {
			name = "source"
		}
		t.Run(name, func(t *testing.T) {
			sourceDir, fontDir := t.TempDir(), t.TempDir()
			source := filepath.Join(sourceDir, "Example.otf")
			fifo := filepath.Join(fontDir, "Example.otf")
			if atSource {
				fifo = source
			} else if err := os.WriteFile(source, []byte("font"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := Install(fontDir, []string{source}, true, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("FIFO accepted as a font")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("font inspection blocked on FIFO")
			}
			if info, err := os.Stat(fifo); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("FIFO changed: info=%v err=%v", info, err)
			}
			entries, err := os.ReadDir(fontDir)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if atSource {
				want = 0
			}
			if len(entries) != want {
				t.Fatalf("staging files retained: %v", entries)
			}
		})
	}
}
