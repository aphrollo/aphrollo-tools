package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Upgrading the gate in place is the one operation the obvious sequence gets
// wrong. Windows refuses to delete or overwrite a running executable but
// allows it to be RENAMED, so the only order that works is: build beside it,
// rename the running copy aside, move the new one into its name. Doing it by
// hand means remembering that every time, and forgetting it leaves a half
// upgrade — a `.new.exe` nobody runs, or hooks pointing at a binary that no
// longer exists.
//
// So it is one verb, and it prints one line per step: an operator who sees
// the run stop knows which step failed and what state the box is in.

// buildAphrollo compiles the binary to out from the module at repo, returning
// the command it ran so the step line can name it. A var so a test can state
// the build's outcome without a toolchain run.
//
// -buildvcs=false: the build routinely runs from a linked worktree with a
// dirty index, where stamping VCS info either fails outright or embeds the
// wrong revision.
var buildAphrollo = func(repo, out string) (string, error) {
	args := []string{"build", "-buildvcs=false", "-o", out, "./cmd/aphrollo"}
	cmd := exec.Command("go", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	desc := "go " + strings.Join(args, " ")
	if err := cmd.Run(); err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return desc, fmt.Errorf("%s: %w\n%s", desc, err, said)
		}
		return desc, fmt.Errorf("%s: %w", desc, err)
	}
	return desc, nil
}

// stalePrefix names the renamed-aside copies, so the sweep can find them and
// nothing else in the bin dir is ever a candidate for deletion.
const stalePrefix = ".stale-"

// runGateSelfInstall rebuilds this binary from source, puts it in place of the
// installed one, reclaims what earlier upgrades left, and rewires the hooks.
func runGateSelfInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("self-install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		binPath = fs.String("bin", "", "binary to replace (default: this executable)")
		repo    = fs.String("repo", ".", "module to build ./cmd/aphrollo from")
		noInit  = fs.Bool("no-init", false, "replace the binary only; skip `gate init`")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	bin := *binPath
	if bin == "" {
		bin = defaultBinPath()
	}

	staged := siblingPath(bin, ".new")
	// A leftover from an upgrade that died mid-flight would otherwise be
	// moved into place as if it were this run's build.
	_ = os.Remove(staged)
	desc, err := buildAphrollo(*repo, staged)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate self-install: build failed, nothing was replaced\n%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "gate self-install: build  %s -> %s\n", desc, staged)

	stale := siblingPath(bin, fmt.Sprintf("%s%d", stalePrefix, time.Now().Unix()))
	renamed := false
	if _, err := os.Stat(bin); err == nil {
		if err := os.Rename(bin, stale); err != nil {
			fmt.Fprintf(stderr, "aphrollo gate self-install: cannot move %s aside: %v\n", bin, err)
			return 1
		}
		renamed = true
		fmt.Fprintf(stdout, "gate self-install: rename %s -> %s\n", bin, stale)
	} else {
		fmt.Fprintf(stdout, "gate self-install: rename skipped, no binary at %s yet\n", bin)
	}

	if err := os.Rename(staged, bin); err != nil {
		// Put the box back the way it was: a bin dir with no binary at all is
		// worse than one running the previous build.
		if renamed {
			_ = os.Rename(stale, bin)
		}
		fmt.Fprintf(stderr, "aphrollo gate self-install: cannot move %s into place: %v\n", staged, err)
		return 1
	}
	fmt.Fprintf(stdout, "gate self-install: move   %s -> %s\n", staged, bin)

	removed, held := sweepStaleBinaries(filepath.Dir(bin), filepath.Base(bin), stale)
	fmt.Fprintf(stdout, "gate self-install: sweep  %d stale copy/copies reclaimed, %d still in use\n", removed, held)

	if *noInit {
		return 0
	}
	// Everything after a bare `--` is forwarded verbatim to init, so
	// `--config-dir`, `--git-hooks-dir` and friends reach it without
	// self-install having to restate every one of them.
	return runGateInit(append([]string{"--bin", bin}, fs.Args()...), stdout, stderr)
}

// siblingPath spells a name beside bin: `aphrollo.exe` + ".new" is
// `aphrollo.new.exe`, so the staging and stale copies keep the extension the
// OS needs to treat them as executables.
func siblingPath(bin, infix string) string {
	dir, name := filepath.Split(bin)
	ext := filepath.Ext(name)
	return filepath.Join(dir, strings.TrimSuffix(name, ext)+infix+ext)
}

// sweepStaleBinaries deletes the copies earlier upgrades renamed aside, and
// reports how many it reclaimed and how many it could not. keep is this run's
// own stale copy: on Windows that IS the running process, so it is never a
// candidate — the NEXT upgrade reclaims it, which is what makes the sweep
// converge instead of growing.
//
// A copy that will not delete is not an error. The whole point of the rename
// dance is that a running binary cannot be removed, and failing the upgrade
// over a file the OS is holding would make the operation impossible to
// perform from the binary being upgraded.
func sweepStaleBinaries(dir, binName, keep string) (removed, held int) {
	ext := filepath.Ext(binName)
	prefix := strings.TrimSuffix(binName, ext) + stalePrefix
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if path == keep {
			continue
		}
		if os.Remove(path) == nil {
			removed++
		} else {
			held++
		}
	}
	return removed, held
}
