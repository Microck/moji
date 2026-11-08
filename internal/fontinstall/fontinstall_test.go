package fontinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() { refresh = func(context.Context, string) {} }

func TestInstallCopiesAndReusesIdenticalFiles(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), filepath.Join(t.TempDir(), "fonts")
	source := filepath.Join(sourceDir, "Example.otf")
	if err := os.WriteFile(source, []byte("OTTO-font"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := Install(fontDir, []string{source}, false, nil)
	if err != nil || len(results) != 1 || results[0].Existing {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	content, err := os.ReadFile(filepath.Join(fontDir, "Example.otf"))
	if err != nil || string(content) != "OTTO-font" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	results, err = Install(fontDir, []string{source}, false, nil)
	if err != nil || !results[0].Existing {
		t.Fatalf("second install results=%+v err=%v", results, err)
	}
}

func TestInstallRefusesToOverwriteDifferentFont(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "Example.otf")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(fontDir, "Example.otf")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Install(fontDir, []string{source}, false, nil)
	var conflict ConflictError
	if !errors.As(err, &conflict) || strings.Join(conflict.Names, ",") != "Example.otf" {
		t.Fatalf("err=%v", err)
	}
	if content, _ := os.ReadFile(existing); string(content) != "old" {
		t.Fatalf("existing font was overwritten: %q", content)
	}
	if _, err := Install(fontDir, []string{source}, true, nil); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(existing); string(content) != "new" {
		t.Fatalf("overwrite kept old content: %q", content)
	}
}

func TestInstallConflictCopiesNothing(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	fresh := filepath.Join(sourceDir, "A.otf")
	clash := filepath.Join(sourceDir, "B.otf")
	for path, content := range map[string]string{fresh: "a", clash: "b", filepath.Join(fontDir, "B.otf"): "old"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install(fontDir, []string{fresh, clash}, false, nil); err == nil {
		t.Fatal("conflict was not reported")
	}
	if _, err := os.Stat(filepath.Join(fontDir, "A.otf")); !os.IsNotExist(err) {
		t.Fatalf("a conflicting install still copied A.otf: %v", err)
	}
}

func TestInstallReconcilesSourcesSharingADestination(t *testing.T) {
	first, second, fontDir := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(directory, content string) string {
		path := filepath.Join(directory, "Example.otf")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	left, right := write(first, "OTTO-same"), write(second, "OTTO-same")
	results, err := Install(fontDir, []string{left, right}, false, nil)
	if err != nil || len(results) != 1 {
		t.Fatalf("identical duplicates: results=%+v err=%v", results, err)
	}

	otherDir := t.TempDir()
	differing := write(t.TempDir(), "OTTO-other")
	extra := filepath.Join(first, "Extra.otf")
	if err := os.WriteFile(extra, []byte("OTTO-extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(otherDir, []string{extra, left, differing}, true, nil); err == nil {
		t.Fatal("differing sources for one destination were accepted")
	}
	if entries, _ := os.ReadDir(otherDir); len(entries) != 0 {
		t.Fatalf("rejected plan left a partial install: %v", entries)
	}
}

func TestUserFontDirPrefersXDGWithoutHome(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" || runtime.GOOS == "plan9" ||
		runtime.GOOS == "android" || runtime.GOOS == "ios" || runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("XDG_DATA_HOME only applies to fontconfig platforms")
	}
	data := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", data)
	directory, ok := UserFontDir()
	if !ok || directory != filepath.Join(data, "fonts") {
		t.Fatalf("directory=%q ok=%v", directory, ok)
	}
}

func TestSupportsRejectsWebFontsOnMacOS(t *testing.T) {
	for _, name := range []string{"a.woff", "b.WOFF2"} {
		if supports("darwin", name) {
			t.Errorf("%s reported installable on macOS", name)
		}
		if !supports("linux", name) {
			t.Errorf("%s reported uninstallable on linux", name)
		}
	}
	if !supports("darwin", "c.otf") {
		t.Error("otf reported uninstallable on macOS")
	}
}

func TestEqualFilesStreamsAllBytes(t *testing.T) {
	left, right := filepath.Join(t.TempDir(), "left"), filepath.Join(t.TempDir(), "right")
	content := bytes.Repeat([]byte("font"), 20*1024)
	for _, path := range []string{left, right} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if same, err := equalFiles(left, right); err != nil || !same {
		t.Fatalf("matching multi-buffer files: same=%v err=%v", same, err)
	}
	content[len(content)-1] = 'x'
	if err := os.WriteFile(right, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if same, err := equalFiles(left, right); err != nil || same {
		t.Fatalf("different final buffer: same=%v err=%v", same, err)
	}
	if err := os.Truncate(right, int64(len(content)-1)); err != nil {
		t.Fatal(err)
	}
	if same, err := equalFiles(left, right); err != nil || same {
		t.Fatalf("different sizes: same=%v err=%v", same, err)
	}
}

func TestInstallStagesAllSourcesBeforeCommitting(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	var sources []string
	for _, name := range []string{"A.otf", "B.otf"} {
		path := filepath.Join(sourceDir, name)
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, path)
	}
	results, err := Install(fontDir, sources, false, func(done, _ int, refreshing bool) {
		if done == 0 && !refreshing {
			if err := os.WriteFile(sources[1], []byte("changed after planning"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("results=%v err=%v", results, err)
	}
	if content, err := os.ReadFile(filepath.Join(fontDir, "B.otf")); err != nil || string(content) != "original" {
		t.Fatalf("did not install the checked snapshot: content=%q err=%v", content, err)
	}
	if entries, err := os.ReadDir(fontDir); err != nil || len(entries) != 2 {
		t.Fatalf("staging files were not removed: entries=%v err=%v", entries, err)
	}
}

func TestInstallStagingFailureCopiesNothing(t *testing.T) {
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "A.otf")
	if err := os.WriteFile(source, []byte("font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(fontDir, []string{source, filepath.Join(sourceDir, "missing.otf")}, false, nil); err == nil {
		t.Fatal("missing source was accepted")
	}
	if entries, err := os.ReadDir(fontDir); err != nil || len(entries) != 0 {
		t.Fatalf("staging failure left files behind: entries=%v err=%v", entries, err)
	}
}

func TestInstallBatchMemoryIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("writes an 8 MiB batch")
	}
	sourceDir, fontDir := t.TempDir(), t.TempDir()
	var sources []string
	for index := range 4 {
		path := filepath.Join(sourceDir, fmt.Sprintf("font-%d.otf", index))
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(2 << 20); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, path)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	results, err := Install(fontDir, sources, false, nil)
	runtime.ReadMemStats(&after)
	if err != nil || len(results) != len(sources) {
		t.Fatalf("results=%v err=%v", results, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("8 MiB batch allocated %d bytes; expected bounded streaming buffers", allocated)
	}
}

func TestEquivalentPlanUsesFileIdentity(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "Example.otf")
	alias := filepath.Join(directory, "alias.otf")
	if err := os.WriteFile(path, []byte("font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Skipf("filesystem does not support hard links: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got := equivalentPlan([]plannedCopy{{info: info}}, aliasInfo); got != 0 {
		t.Fatalf("filesystem alias did not reuse its planned destination: %d", got)
	}
}

func TestInstallReconcilesCaseEquivalentDestinations(t *testing.T) {
	probeDir := t.TempDir()
	probe := filepath.Join(probeDir, "CaseProbe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(probeDir, "caseprobe"))
	insensitive := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, identical := range []bool{false, true} {
		for _, overwrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("same=%v/overwrite=%v", identical, overwrite), func(t *testing.T) {
				fontDir := t.TempDir()
				left := filepath.Join(t.TempDir(), "Example.otf")
				right := filepath.Join(t.TempDir(), "example.otf")
				rightContent := "different"
				if identical {
					rightContent = "same"
				}
				for path, content := range map[string]string{left: "same", right: rightContent} {
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				results, err := Install(fontDir, []string{left, right}, overwrite, nil)
				if insensitive && !identical {
					if err == nil || len(results) != 0 {
						t.Fatalf("different aliases accepted: results=%v err=%v", results, err)
					}
					if entries, _ := os.ReadDir(fontDir); len(entries) != 0 {
						t.Fatalf("conflicting aliases left partial installation: %v", entries)
					}
					return
				}
				want := 2
				if insensitive {
					want = 1
				}
				if err != nil || len(results) != want {
					t.Fatalf("results=%v err=%v want=%d", results, err, want)
				}
			})
		}
	}
	t.Logf("case-insensitive filesystem: %v", insensitive)
}

func TestInstallCancellationCleansStagingBeforeReturn(t *testing.T) {
	for _, cancelAfter := range []int{0, 1} {
		t.Run(fmt.Sprint(cancelAfter), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sourceDir, fontDir := t.TempDir(), t.TempDir()
			var sources []string
			for _, name := range []string{"A.otf", "B.otf"} {
				path := filepath.Join(sourceDir, name)
				if err := os.WriteFile(path, []byte("font"), 0o600); err != nil {
					t.Fatal(err)
				}
				sources = append(sources, path)
			}
			results, err := InstallContext(ctx, fontDir, sources, false, func(done, _ int, refreshing bool) {
				if done == cancelAfter && !refreshing {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) || len(results) != cancelAfter {
				t.Fatalf("results=%v err=%v", results, err)
			}
			entries, err := os.ReadDir(fontDir)
			if err != nil || len(entries) != cancelAfter {
				t.Fatalf("cancellation left staging files: entries=%v err=%v", entries, err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".moji-stage-") {
					t.Fatalf("staging directory retained: %s", entry.Name())
				}
			}
		})
	}
}

func TestContextReaderStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := contextReader{ctx: ctx, reader: strings.NewReader("font data")}
	buffer := make([]byte, 4)
	if n, err := reader.Read(buffer); n != 4 || err != nil {
		t.Fatalf("first read: n=%d err=%v", n, err)
	}
	cancel()
	if n, err := reader.Read(buffer); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: n=%d err=%v", n, err)
	}
}

func TestInstallRejectsDirectoryDestination(t *testing.T) {
	source, fonts := filepath.Join(t.TempDir(), "Example.otf"), t.TempDir()
	if err := os.WriteFile(source, []byte("font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(fonts, "Example.otf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(fonts, []string{source}, true, nil); err == nil {
		t.Fatal("directory destination accepted")
	}
	entries, err := os.ReadDir(fonts)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("destination changed or staging retained: entries=%v err=%v", entries, err)
	}
}

// Cancel at a deterministic read checkpoint without timing or oversized files.
type cancelAfterChecks struct {
	context.Context
	cancel          context.CancelFunc
	allowed, checks int
}

func (ctx *cancelAfterChecks) Err() error {
	ctx.checks++
	if ctx.checks > ctx.allowed {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestStagingAndComparisonCancelBetweenReadChunks(t *testing.T) {
	content := bytes.Repeat([]byte("font"), 256*1024)
	source, other := filepath.Join(t.TempDir(), "source.otf"), filepath.Join(t.TempDir(), "other.otf")
	for _, path := range []string{source, other} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("staging", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &cancelAfterChecks{Context: parent, cancel: cancel, allowed: 1}
		staged := filepath.Join(t.TempDir(), "staged.otf")
		if err := stageFont(ctx, source, staged); !errors.Is(err, context.Canceled) {
			t.Fatalf("stage error=%v", err)
		}
		info, err := os.Stat(staged)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() <= 0 || info.Size() >= int64(len(content)) {
			t.Fatalf("staging read all input or none: size=%d", info.Size())
		}
	})
	t.Run("comparison", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &cancelAfterChecks{Context: parent, cancel: cancel, allowed: 2}
		if same, err := equalFilesContext(ctx, source, other); same || !errors.Is(err, context.Canceled) {
			t.Fatalf("same=%v err=%v", same, err)
		}
		if ctx.checks != 4 {
			t.Fatalf("comparison did not stop in its second buffer pair: checks=%d", ctx.checks)
		}
	})
}

func TestPartialCancellationRefreshesAfterCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sourceDir, fonts := t.TempDir(), t.TempDir()
	var sources []string
	for _, name := range []string{"A.otf", "B.otf"} {
		path := filepath.Join(sourceDir, name)
		if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fonts, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, path)
	}
	previous := refresh
	defer func() { refresh = previous }()
	refreshed := false
	refresh = func(refreshCtx context.Context, directory string) {
		refreshed = true
		if refreshCtx.Err() != nil || directory != fonts {
			t.Fatalf("refresh context=%v directory=%q", refreshCtx.Err(), directory)
		}
		deadline, ok := refreshCtx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second || time.Until(deadline) <= 0 {
			t.Fatal("cancellation refresh must have a live two-second limit")
		}
		entries, err := os.ReadDir(fonts)
		if err != nil || len(entries) != 2 {
			t.Fatalf("staging was not cleaned before refresh: entries=%v err=%v", entries, err)
		}
	}
	results, err := InstallContext(ctx, fonts, sources, true, func(done, _ int, refreshing bool) {
		if done == 1 && !refreshing {
			cancel()
		}
	})
	if !refreshed || len(results) != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("refreshed=%v results=%v err=%v", refreshed, results, err)
	}
	for name, want := range map[string]string{"A.otf": "new", "B.otf": "old"} {
		if got, err := os.ReadFile(filepath.Join(fonts, name)); err != nil || string(got) != want {
			t.Fatalf("%s=%q err=%v", name, got, err)
		}
	}
}

func TestCancellationDuringRefreshGetsBoundedRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source, fonts := filepath.Join(t.TempDir(), "font.otf"), t.TempDir()
	if err := os.WriteFile(source, []byte("font"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := refresh
	defer func() { refresh = previous }()
	calls := 0
	refresh = func(refreshCtx context.Context, _ string) {
		calls++
		if calls == 1 {
			cancel()
			return
		}
		deadline, bounded := refreshCtx.Deadline()
		if refreshCtx.Err() != nil || !bounded || time.Until(deadline) > 2*time.Second {
			t.Fatal("refresh retry was canceled or unbounded")
		}
	}
	results, err := InstallContext(ctx, fonts, []string{source}, false, nil)
	if calls != 2 || len(results) != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d results=%v err=%v", calls, results, err)
	}
}

func TestCleanupFailureStillRefreshesCommittedFonts(t *testing.T) {
	for _, cancelAfterCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%v", cancelAfterCommit), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sources, fonts := t.TempDir(), t.TempDir()
			var paths []string
			for _, name := range []string{"A.otf", "B.otf"} {
				path := filepath.Join(sources, name)
				if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(fonts, name), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, path)
			}
			cleanupErr := errors.New("staging cleanup denied")
			removed, refreshed := false, false
			previous := refresh
			defer func() { refresh = previous }()
			refresh = func(refreshCtx context.Context, directory string) {
				refreshed = true
				if !removed || refreshCtx.Err() != nil || directory != fonts {
					t.Fatalf("removed=%v context=%v directory=%q", removed, refreshCtx.Err(), directory)
				}
				if cancelAfterCommit {
					deadline, bounded := refreshCtx.Deadline()
					if !bounded || time.Until(deadline) > 2*time.Second {
						t.Fatal("unbounded cancellation refresh")
					}
				}
			}
			results, err := installContext(ctx, fonts, paths, true, func(done, _ int, refreshing bool) {
				if cancelAfterCommit && done == 1 && !refreshing {
					cancel()
				}
			}, func(staging string) error {
				removed = true
				if filepath.Dir(staging) != fonts || !strings.HasPrefix(filepath.Base(staging), ".moji-stage-") {
					t.Fatalf("staging must remain hidden from fontconfig: %q", staging)
				}
				return cleanupErr
			})
			if !refreshed || !errors.Is(err, cleanupErr) || len(results) == 0 {
				t.Fatalf("refreshed=%v results=%v error=%v", refreshed, results, err)
			}
			if cancelAfterCommit && (!errors.Is(err, context.Canceled) || len(results) != 1) {
				t.Fatalf("partial cancellation lost: results=%v error=%v", results, err)
			}
		})
	}
}
