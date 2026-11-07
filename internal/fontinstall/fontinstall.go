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
	if runtime.GOOS != "darwin" {
		if data := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
			return filepath.Join(data, "fonts"), true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Fonts"), true
	}
	return filepath.Join(home, ".local", "share", "fonts"), true
}

// Supports reports whether copying path into the user font directory installs
// it. macOS can't install WOFF or WOFF2 web fonts; they must be converted to
// TTF or OTF first.
func Supports(path string) bool {
	return supports(runtime.GOOS, path)
}

func supports(goos, path string) bool {
	if goos != "darwin" {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".woff", ".woff2":
		return false
	}
	return true
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

type plannedCopy struct {
	source, destination, staged string
	info                        os.FileInfo
	existing, replace           bool
}

func Install(directory string, sources []string, overwrite bool, progress Progress) ([]Result, error) {
	return InstallContext(context.Background(), directory, sources, overwrite, progress)
}

// InstallContext is Install with cancellation. It returns only after removing
// its staging directory (or reporting a cleanup error). Already committed fonts
// are retained on cancellation.
func InstallContext(ctx context.Context, directory string, sources []string, overwrite bool, progress Progress) (results []Result, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if progress == nil {
		progress = func(int, int, bool) {}
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("Moji couldn't create the font directory %s: %w", directory, err)
	}
	// Keep snapshots on the destination filesystem, not in memory. Creating the
	// desired basenames here also lets that filesystem resolve case and Unicode
	// aliases before any installed font is changed.
	staging, err := os.MkdirTemp(directory, ".moji-stage-*")
	if err != nil {
		return nil, fmt.Errorf("Moji couldn't stage fonts in %s: %w", directory, err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("Moji couldn't remove staging directory %s: %w", staging, err))
		}
	}()
	plans := make([]plannedCopy, 0, len(sources))
	var conflicts []string
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := filepath.Base(source)
		plan := plannedCopy{source: source, destination: filepath.Join(directory, name), staged: filepath.Join(staging, name)}
		if err := stageFont(ctx, source, plan.staged); errors.Is(err, os.ErrExist) {
			info, statErr := os.Stat(plan.staged)
			if statErr != nil {
				return nil, fmt.Errorf("Moji couldn't inspect staged font %s: %w", name, statErr)
			}
			earlier := equivalentPlan(plans, info)
			if earlier < 0 {
				return nil, fmt.Errorf("Moji couldn't reconcile staged font %s", name)
			}
			same, compareErr := equalFilesContext(ctx, plan.staged, source)
			if compareErr != nil {
				return nil, fmt.Errorf("Moji couldn't compare %s: %w", source, compareErr)
			}
			if !same {
				return nil, fmt.Errorf("Moji couldn't install %s and %s: both are named %s but differ. Nothing was installed; rename one and try again", plans[earlier].source, source, name)
			}
			continue
		} else if err != nil {
			return nil, fmt.Errorf("Moji couldn't stage %s: %w", source, err)
		}
		plan.info, err = os.Stat(plan.staged)
		if err != nil {
			return nil, fmt.Errorf("Moji couldn't inspect staged font %s: %w", name, err)
		}
		if same, err := equalFilesContext(ctx, plan.staged, plan.destination); err == nil {
			if same {
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
	results = make([]Result, 0, len(plans))
	copied := false
	for index, plan := range plans {
		progress(index, len(plans), false)
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if !plan.existing {
			if err := writeFont(plan.staged, plan.destination, plan.replace); err != nil {
				if copied {
					_ = os.RemoveAll(staging)
					progress(index, len(plans), true)
					refresh(ctx, directory)
				}
				return results, err
			}
			copied = true
		}
		results = append(results, Result{Source: plan.source, Path: plan.destination, Existing: plan.existing})
	}
	progress(len(plans), len(plans), false)
	if copied {
		_ = os.RemoveAll(staging)
		progress(len(plans), len(plans), true)
		refresh(ctx, directory)
	}
	return results, ctx.Err()
}

// stageFont snapshots one regular file with bounded memory and exclusive
// creation, preserving the filesystem's own filename-equivalence rules.
func stageFont(ctx context.Context, source, staged string) error {
	input, err := openRegularFile(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", source)
	}
	output, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, contextReader{ctx: ctx, reader: input})
	return errors.Join(copyErr, output.Close())
}

func equivalentPlan(plans []plannedCopy, info os.FileInfo) int {
	for index, plan := range plans {
		if os.SameFile(plan.info, info) {
			return index
		}
	}
	return -1
}

// writeFont exposes an already staged snapshot in one step. A file that
// appeared after planning is left untouched unless replacement was approved.
func writeFont(staged, destination string, replace bool) error {
	if err := os.Chmod(staged, 0o644); err != nil {
		return fmt.Errorf("Moji couldn't set permissions on %s: %w", destination, err)
	}
	if replace {
		if err := os.Rename(staged, destination); err != nil {
			return fmt.Errorf("Moji couldn't replace %s: %w", destination, err)
		}
		return nil
	}
	if err := filecommit.MoveNoReplace(staged, destination); err != nil {
		return fmt.Errorf("Moji couldn't install %s: %w", destination, err)
	}
	return nil
}

// equalFiles compares regular files with fixed-size buffers instead of
// retaining either file (or an entire batch of fonts) in memory.
func equalFiles(left, right string) (bool, error) {
	return equalFilesContext(context.Background(), left, right)
}

func equalFilesContext(ctx context.Context, left, right string) (bool, error) {
	leftFile, err := openRegularFile(left)
	if err != nil {
		return false, err
	}
	defer leftFile.Close()
	rightFile, err := openRegularFile(right)
	if err != nil {
		return false, err
	}
	defer rightFile.Close()
	leftInfo, err := leftFile.Stat()
	if err != nil {
		return false, err
	}
	rightInfo, err := rightFile.Stat()
	if err != nil {
		return false, err
	}
	if !leftInfo.Mode().IsRegular() || !rightInfo.Mode().IsRegular() {
		return false, fmt.Errorf("font comparison requires regular files")
	}
	if leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	var leftBuffer, rightBuffer [32 * 1024]byte
	for {
		leftN, leftErr := io.ReadFull(contextReader{ctx: ctx, reader: leftFile}, leftBuffer[:])
		rightN, rightErr := io.ReadFull(contextReader{ctx: ctx, reader: rightFile}, rightBuffer[:])
		for _, readErr := range []error{leftErr, rightErr} {
			if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
				return false, readErr
			}
		}
		if leftN != rightN || !bytes.Equal(leftBuffer[:leftN], rightBuffer[:rightN]) {
			return false, nil
		}
		if leftErr != nil || rightErr != nil {
			return leftErr == rightErr, nil
		}
	}
}

// refresh is replaceable in tests so they never run the real fc-cache.
var refresh = refreshCache

// refreshCache asks fontconfig to pick up the new files. It is best effort:
// fontconfig rescans directories on its own, so a missing or failing fc-cache
// only delays when applications notice the fonts.
func refreshCache(parent context.Context, directory string) {
	if runtime.GOOS == "darwin" {
		return
	}
	path, err := exec.LookPath("fc-cache")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, path, "-f", directory).Run()
}

// Check cancellation between bounded reads; do not let io.Copy select a
// filesystem fast path that skips these checks.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func openRegularFile(path string) (*os.File, error) {
	file, err := openNonblocking(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return file, nil
}
