// Package fontinstall copies downloaded fonts into the current user's font
// directory so desktop applications can use them without a manual copy.
package fontinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/microck/moji/internal/filecommit"
)

// UserFontDir returns the per-user font directory for the current platform.
// It returns false on platforms where copying a file is not enough to install
// a font (Windows also needs registry entries), so callers can skip offering
// the install step there.
func UserFontDir() (string, bool) {
	switch runtime.GOOS {
	case "windows", "android", "ios", "js", "wasip1", "plan9":
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Fonts"), true
	}
	if data := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
		return filepath.Join(data, "fonts"), true
	}
	return filepath.Join(home, ".local", "share", "fonts"), true
}

// Result describes one installed file.
type Result struct {
	Source string
	Path   string
	// Existing is true when an identical file was already installed.
	Existing bool
}

// ConflictError reports installed fonts that share a name with a download but
// have different content. Install returns it before copying anything, so the
// caller can ask whether to overwrite and retry with overwrite set.
type ConflictError struct {
	Directory string
	Names     []string
}

// ConflictNames lists the clashing filenames.
func (conflict ConflictError) ConflictNames() []string { return conflict.Names }

func (conflict ConflictError) Error() string {
	return fmt.Sprintf("%s already contains a different font named %s", conflict.Directory, strings.Join(conflict.Names, ", "))
}

// Install copies every source file into directory. Identical files are reused.
// Unless overwrite is true, a different file with the same name stops the
// install with a ConflictError before any file is copied. After copying it
// refreshes the fontconfig cache when fc-cache is available.
// Progress reports that done of total files are copied. refreshing is true
// while the font cache is being rebuilt.
type Progress func(done, total int, refreshing bool)

func Install(directory string, sources []string, overwrite bool, progress Progress) ([]Result, error) {
	if progress == nil {
		progress = func(int, int, bool) {}
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("Moji couldn't create the font directory %s: %w", directory, err)
	}
	type plannedCopy struct {
		source, destination string
		content             []byte
		existing, replace   bool
	}
	plans := make([]plannedCopy, 0, len(sources))
	var conflicts []string
	for _, source := range sources {
		content, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("Moji couldn't read %s: %w", source, err)
		}
		name := filepath.Base(source)
		plan := plannedCopy{source: source, destination: filepath.Join(directory, name), content: content}
		if same, err := sameFile(source, plan.destination); err == nil && same {
			plan.existing = true
		} else if installed, err := os.ReadFile(plan.destination); err == nil {
			if bytes.Equal(installed, content) {
				plan.existing = true
			} else {
				plan.replace = true
				conflicts = append(conflicts, name)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Moji couldn't inspect %s: %w", plan.destination, err)
		}
		plans = append(plans, plan)
	}
	if len(conflicts) > 0 && !overwrite {
		return nil, ConflictError{Directory: directory, Names: conflicts}
	}
	results := make([]Result, 0, len(plans))
	copied := false
	for index, plan := range plans {
		progress(index, len(plans), false)
		if !plan.existing {
			if err := writeFont(directory, plan.destination, plan.content, plan.replace); err != nil {
				if copied {
					progress(index, len(plans), true)
					refresh(directory)
				}
				return results, err
			}
			copied = true
		}
		results = append(results, Result{Source: plan.source, Path: plan.destination, Existing: plan.existing})
	}
	progress(len(plans), len(plans), false)
	if copied {
		progress(len(plans), len(plans), true)
		refresh(directory)
	}
	return results, nil
}

// writeFont stages content beside destination and then exposes it in one
// step. replace allows swapping out an existing file; otherwise a file that
// appeared in the meantime is left untouched.
func writeFont(directory, destination string, content []byte, replace bool) error {
	temporary, err := os.CreateTemp(directory, ".moji-*.tmp")
	if err != nil {
		return fmt.Errorf("Moji couldn't write to %s: %w. Check the directory permissions", directory, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	_, writeErr := io.Copy(temporary, bytes.NewReader(content))
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("Moji couldn't write %s: %w", destination, errors.Join(writeErr, closeErr))
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return fmt.Errorf("Moji couldn't set permissions on %s: %w", destination, err)
	}
	if replace {
		if err := os.Rename(temporaryPath, destination); err != nil {
			return fmt.Errorf("Moji couldn't replace %s: %w", destination, err)
		}
		return nil
	}
	if err := filecommit.MoveNoReplace(temporaryPath, destination); err != nil {
		return fmt.Errorf("Moji couldn't install %s: %w", destination, err)
	}
	return nil
}

func sameFile(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, err
	}
	return os.SameFile(leftInfo, rightInfo), nil
}

// refresh is replaceable in tests so they never run the real fc-cache.
var refresh = refreshCache

// refreshCache asks fontconfig to pick up the new files. It is best effort:
// fontconfig rescans directories on its own, so a missing or failing fc-cache
// only delays when applications notice the fonts.
func refreshCache(directory string) {
	if runtime.GOOS == "darwin" {
		return
	}
	path, err := exec.LookPath("fc-cache")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, path, "-f", directory).Run()
}
