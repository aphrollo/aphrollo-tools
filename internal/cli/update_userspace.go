package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// installUserSpace builds the tag's binary from the worktree at tmp, proves it
// with the same smoke check an in-place swap runs, installs it as its own
// version directory, then moves the pointer and prunes. The pointer moves
// last: a build or check that fails leaves every installed version, and the
// version hooks run, exactly as they were. No root and no running image is
// touched, so this is also how Windows replaces a binary it cannot overwrite.
func installUserSpace(root, version, tag, tmp string, noInit bool, forwarded []string, stdout, stderr io.Writer) int {
	const prefix = "aphrollo update"
	if err := os.MkdirAll(root, 0o755); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
		return 1
	}
	sweepStageDirs(root)
	stage, err := os.MkdirTemp(root, ".stage-")
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
		return 1
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "staged"+userbin.ExeSuffix)
	desc, err := buildAphrollo(tmp, staged)
	if err != nil {
		fmt.Fprintf(stderr, "%s: build failed, nothing was installed\n%v\n", prefix, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: build  %s -> %s\n", prefix, desc, staged)
	if err := runSmokeCheckFn(staged); err != nil {
		fmt.Fprintf(stderr, "%s: %s failed its own smoke check, nothing was installed: %v\n", prefix, staged, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: smoke  %s passed selfcheck\n", prefix, staged)
	installed, err := userbin.Install(root, version, staged)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: install %s\n", prefix, installed)
	before, _ := userbin.Current(root)
	if err := userbin.SetCurrent(root, version); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
		return 1
	}
	removed, held := userbin.Prune(root, userbin.DefaultKeep)
	fmt.Fprintf(stdout, "%s: prune  %d old version(s) removed, %d still in use\n", prefix, len(removed), len(held))
	if before == "" {
		before = buildinfo.Version()
	}
	fmt.Fprintf(stdout, "%s: v%s -> %s\n", prefix, before, tag)
	afterPointerMoved(root, prefix, noInit, stdout, stderr)
	if noInit {
		return 0
	}
	return initAfterSwap(prefix, installed, userSpaceInitArgs(root, forwarded), stdout, stderr)
}

// userSpaceInitArgs is what `gate init` is told after a user-space install:
// the binary this process is running, when it lies outside the user-space
// root, stays the hooks' second choice.
func userSpaceInitArgs(root string, forwarded []string) []string {
	var args []string
	if exe := rawExecutablePath(); !userbin.Under(root, exe) {
		args = append(args, "--fallback-bin", exe)
	}
	return append(args, forwarded...)
}

// switchUserSpace points the user-space current at an installed version: no
// fetch, no build, the other versions untouched.
func switchUserSpace(version string, noInit bool, forwarded []string, stdout, stderr io.Writer) int {
	const prefix = "aphrollo update"
	root, err := userbin.Root()
	if err != nil {
		fmt.Fprintf(stderr, "%s: no user-space install dir: %v\n", prefix, err)
		return 1
	}
	have := userbin.Versions(root)
	if !slices.Contains(have, version) {
		fmt.Fprintf(stderr, "%s: --to %s: not installed under %s; installed: %s\n", prefix, version, root, strings.Join(have, ", "))
		return 1
	}
	before, _ := userbin.Current(root)
	if before == "" {
		before = "none"
	}
	if err := userbin.SetCurrent(root, version); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
		return 1
	}
	bin := userbin.BinaryPath(root, version)
	fmt.Fprintf(stdout, "%s: switched v%s -> v%s (%s)\n", prefix, before, version, bin)
	afterPointerMoved(root, prefix, noInit, stdout, stderr)
	if noInit {
		return 0
	}
	return initAfterSwap(prefix, bin, userSpaceInitArgs(root, forwarded), stdout, stderr)
}

// afterPointerMoved is what follows a moved pointer: the launcher a person
// types `aphrollo` through is refreshed, one line says when PATH would run
// something else (PATH and shell startup files are never edited here), and a
// --no-init on Windows says the queue shim copies are one version behind.
func afterPointerMoved(root, prefix string, noInit bool, stdout, stderr io.Writer) {
	fallback := userbin.LegacyFallback()
	if exe := rawExecutablePath(); !userbin.Under(root, exe) {
		fallback = exe
	}
	if _, err := userbin.WriteLauncher(root, fallback); err != nil {
		fmt.Fprintf(stderr, "%s: could not write the launcher in %s: %v\n", prefix, root, err)
	} else if line := userbin.PathCheck(root); line != "" {
		fmt.Fprintf(stdout, "%s: %s\n", prefix, line)
	}
	if noInit && binGOOS == "windows" {
		fmt.Fprintf(stdout, "%s: --no-init left the queue shims (cargo.exe, git.exe) copies of the previous binary; run `aphrollo gate init` to refresh them\n", prefix)
	}
}

// staleStageAge is how old a staging directory must be before an update
// removes it: a younger one may be another update's.
const staleStageAge = 24 * time.Hour

// sweepStageDirs removes staging directories an interrupted update left.
func sweepStageDirs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".stage-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleStageAge {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
