package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
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

// buildArgs is the `go` argv buildAphrollo runs. Pure so the stamping
// logic can be tested without a toolchain run: sha and now are passed in
// rather than discovered here.
//
// -buildvcs=false: the build routinely runs from a linked worktree with a
// dirty index, where stamping VCS info either fails outright or embeds the
// wrong revision. The commit and build time are stamped instead through
// -ldflags -X into internal/buildinfo, which `aphrollo version` reads back —
// the thing -buildvcs=false took away.
func buildArgs(repo, out, sha string, now time.Time) []string {
	ldflags := fmt.Sprintf(
		"-X github.com/aphrollo/aphrollo-tools/internal/buildinfo.commit=%s -X github.com/aphrollo/aphrollo-tools/internal/buildinfo.builtAt=%s",
		sha, now.UTC().Format(time.RFC3339),
	)
	return []string{"build", "-buildvcs=false", "-ldflags", ldflags, "-o", out, "./cmd/aphrollo"}
}

// commitAt returns the current HEAD sha of repo, or "" if it cannot be
// determined (not a git checkout, git not on PATH, etc). A failure here must
// not fail the build — it only means the binary comes out unstamped.
func commitAt(repo string) string {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// buildAphrollo compiles the binary to out from the module at repo, returning
// the command it ran so the step line can name it. A var so a test can state
// the build's outcome without a toolchain run.
var buildAphrollo = func(repo, out string) (string, error) {
	args := buildArgs(repo, out, commitAt(repo), time.Now())
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

// selfCheckArgs is the argv swapBinary runs against a candidate before it
// replaces the installed binary: exit 0 means the candidate's own
// FindProjectRoot still refuses a marker-less tree (issue #532); anything
// else means installing it would repeat that regression.
var selfCheckArgs = []string{"gate", "selfcheck"}

// runSmokeCheckFn indirects the actual subprocess spawn so a test can force
// pass/fail without a real aphrollo binary on disk. This package's own suite
// never builds one — buildAphrollo is stubbed everywhere it is reached — so
// TestMain defaults this to permissive and only the refusal test overrides
// it locally.
var runSmokeCheckFn = func(candidate string) error {
	cmd := exec.Command(candidate, selfCheckArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", candidate, strings.Join(selfCheckArgs, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// `gate self-install` lived here. It built ./cmd/aphrollo from an ARBITRARY
// checkout and made the result the box's binary — the one capability
// `aphrollo update` does not have — and it existed to paper over one
// bootstrap: the fixtures stage judged a lane's laws with the INSTALLED
// binary, so a matcher correction could not commit until the box already
// carried it, and the way out was to move a machine-wide binary to unmerged
// code (#659, #673). The stage now builds the lane for the laws the lane
// itself changed, so the bootstrap is gone and so is the reason. Everything
// below is the swap and init machinery `aphrollo update` still uses; the one
// thing removed is the path that could point it at an unmerged tree.

// runInstalledInitFn indirects the spawn of the freshly installed binary for
// the post-swap `gate init`, so a test can state that step's outcome without
// a real build on disk. It reports the child's exit code alongside the error,
// because that code is what the installing verb exits with.
var runInstalledInitFn = func(bin string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(bin, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exit.ExitCode(), err
	}
	return 1, err
}

// initAfterSwap runs `gate init` under the binary that was JUST installed,
// never in this process. Every managed file init writes -- .ratchet/README.md,
// the CLAUDE.md block, the hook and queue shims -- comes from templates
// EMBEDDED IN THE BINARY, so an in-process init after a swap writes the
// OUTGOING build's templates over content the new build owns. That is not
// theoretical: a box updating from a pre-#621 image rewrote the tracked
// .ratchet/README.md without its path-tombstone paragraph, and the same
// `gate init` under the new binary restored it byte for byte.
//
// bin is the path the swap just wrote, so this never depends on the new
// binary being on PATH. extra is whatever the caller was told to forward
// (--config-dir, --git-hooks-dir and friends).
//
// A failure here is REPORTED, never skipped, and says the swap already
// happened: the binary is new, its managed files are not, and only an
// operator who is told that knows to finish the job by hand.
func initAfterSwap(prefix, bin string, extra []string, stdout, stderr io.Writer) int {
	args := append([]string{"gate", "init", "--bin", bin}, extra...)
	fmt.Fprintf(stdout, "%s: init   %s (under the newly installed binary)\n", prefix, strings.Join(args, " "))
	code, err := runInstalledInitFn(bin, args, stdout, stderr)
	if err == nil {
		return 0
	}
	if code == 0 {
		code = 1
	}
	fmt.Fprintf(stderr, "%s: %s failed under the binary just installed at %s: %v\n", prefix, strings.Join(args, " "), bin, err)
	fmt.Fprintf(stderr, "%s: the box is half-updated -- the swap stands, the managed files were NOT rewritten; re-run `%s gate init` once the cause is fixed\n", prefix, bin)
	return code
}

// renameFn indirects os.Rename inside swapBinary so a test can force the
// exact double-failure sequence (the forward move fails, then the rollback
// meant to restore the previous binary ALSO fails) that a real filesystem
// has no reliable, portable way to reproduce on demand.
var renameFn = os.Rename

// replacedBinaryJobsLineFn indirects tdd.ReplacedBinaryJobsLine so a test can
// state its finding without needing a real live process to point at.
var replacedBinaryJobsLineFn = tdd.ReplacedBinaryJobsLine

// swapBinary renames bin aside (if one exists yet), moves staged into its
// place, and sweeps whatever earlier upgrades left beside it — the sequence
// any verb that replaces the running binary needs, shared so `gate
// installer and its callers behave byte-identically instead of drifting.
// prefix names the caller in the three lines this prints to stdout (e.g.
// "aphrollo update" or "aphrollo install"), so an operator watching either
// verb sees its own name. stale is the path the previous binary was renamed
// to, or "" when there was nothing at bin yet. After the move, this verifies
// bin actually resolves via exec.LookPath before declaring success (#366) —
// a wrong-shaped bin (an extensionless path with no sibling .exe) moves the
// build into place fine and is still not runnable by anything Go spawns.
func swapBinary(prefix, bin, staged string, stdout io.Writer) (stale string, err error) {
	// Checked BEFORE anything is renamed: a refusal here leaves bin exactly
	// as it was, with nothing to roll back (issue #532 — `aphrollo update`
	// used to swap a candidate in without ever proving it could still judge
	// a tree correctly).
	if err := runSmokeCheckFn(staged); err != nil {
		return "", fmt.Errorf("%s: %s failed its own smoke check, keeping %s in place: %w", prefix, staged, bin, err)
	}
	fmt.Fprintf(stdout, "%s: smoke  %s passed selfcheck\n", prefix, staged)

	stale = siblingPath(bin, fmt.Sprintf("%s%d", stalePrefix, time.Now().Unix()))
	renamed := false
	if _, statErr := os.Stat(bin); statErr == nil {
		if err := renameFn(bin, stale); err != nil {
			return "", fmt.Errorf("cannot move %s aside: %w", bin, err)
		}
		renamed = true
		fmt.Fprintf(stdout, "%s: rename %s -> %s\n", prefix, bin, stale)
	} else {
		fmt.Fprintf(stdout, "%s: rename skipped, no binary at %s yet\n", prefix, bin)
		stale = ""
	}

	if err := renameFn(staged, bin); err != nil {
		// Put the box back the way it was: a bin dir with no binary at all is
		// worse than one running the previous build. If the restore ITSELF
		// fails, that must reach the operator too — the earlier code
		// discarded this error, which is exactly how a box can be left with
		// nothing at bin and a report that only mentions the first failure.
		if renamed {
			if rerr := renameFn(stale, bin); rerr != nil {
				return "", fmt.Errorf("cannot move %s into place: %w; restoring the previous binary from %s also failed: %v; %s still holds it, move it back by hand", staged, err, stale, rerr, stale)
			}
		}
		return "", fmt.Errorf("cannot move %s into place: %w", staged, err)
	}
	fmt.Fprintf(stdout, "%s: move   %s -> %s\n", prefix, staged, bin)

	// #366: the move can succeed and still leave nothing spawnable — an
	// extensionless bin on Windows is the case that happened for real. Catch
	// it here, at the moment it happens, instead of letting a caller report
	// success and find out from a spawn failure later.
	if _, err := exec.LookPath(bin); err != nil {
		return stale, fmt.Errorf("%s was moved into place but does not resolve as a runnable binary: %w", bin, err)
	}

	removed, held := sweepStaleBinaries(filepath.Dir(bin), filepath.Base(bin), stale)
	fmt.Fprintf(stdout, "%s: sweep  %d stale copy/copies reclaimed, %d still in use\n", prefix, removed, held)

	// #338: a job still executing the copy just renamed to `stale` holds the
	// box-wide mutation-run lock and produces results from code no longer
	// installed. This is the one moment that fact is free — the installer
	// already knows it just replaced the binary, and every pid is already on
	// record — so it is named here rather than only to whoever queues behind
	// the job later (#311's queue-side notice).
	if line := replacedBinaryJobsLineFn(stale); line != "" {
		fmt.Fprintln(stdout, line)
	}

	return stale, nil
}

// binExtForOS appends the extension Windows executables need when bin was
// given with none, so an explicit --bin cannot produce a binary only a
// human's shell will run (#366) — the earlier bug had `--bin` reach
// siblingPath and swapBinary verbatim, so the staged, stale and installed
// names all lost the extension together. goos is a parameter, rather than
// this reading runtime.GOOS itself, so both platforms' behavior can be
// proven from one machine.
func binExtForOS(bin, goos string) (normalized string, appended bool) {
	if goos != "windows" {
		return bin, false
	}
	if filepath.Ext(bin) != "" {
		return bin, false
	}
	return bin + ".exe", true
}

// binGOOS is the OS resolveBinPath normalizes bin's extension for. A var
// rather than a direct runtime.GOOS read so a test can pin it to "windows"
// and prove the extension logic without the test's own outcome depending on
// which OS actually runs it.
var binGOOS = runtime.GOOS

// resolveBinPath applies --bin (or the running binary's own path when it was
// left blank) and normalizes its extension for the current OS, printing the
// same one-line step style the rest of the swap already uses. This is
// `aphrollo update`'s own resolver — its only caller — and it deliberately
// falls back to rawExecutablePath, NOT defaultBinPath: see rawExecutablePath's
// own doc for why update must never inherit the stable-symlink preference
// `gate init`/`install` use.
func resolveBinPath(binFlag, prefix string, stdout io.Writer) string {
	bin := binFlag
	if bin == "" {
		bin = rawExecutablePath()
	}
	if normalized, appended := binExtForOS(bin, binGOOS); appended {
		fmt.Fprintf(stdout, "%s: bin    %s -> %s (Windows needs the extension to run it)\n", prefix, bin, normalized)
		bin = normalized
	}
	return bin
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
