package tdd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// gitGateHooks are the git hooks the gate manages, paired with the `aphrollo
// tdd` subcommand each shim invokes. install also PRUNES any managed pre-push
// shim it finds (see prunedHooks + uninstallGitGate), so a box only runs the
// hooks listed here. pre-merge-commit was added 2026-08-15 (build-infra-fix
// task A6): `git merge` never fires pre-commit, so a merge landed untested
// unless someone ran the workspace suite by hand — it runs Mechanical only
// (no fail-first/anti-cheat, both already settled on the commits being
// merged).
var gitGateHooks = []struct{ name, sub string }{
	{"pre-commit", "precommit"},
	{"pre-merge-commit", "premerge"},
	// post-commit writes the gate note on the commit just made — what lets CI
	// tell a red on a gated tip from a red on an ungated one — and that is the
	// whole of it. It starts nothing: the mutation measurement runs in the
	// foreground at merge time, on the merged tree. It cannot block either
	// way, because the commit has already happened.
	{"post-commit", "postcommit"},
	// commit-msg fires for EVERY commit, including a non-fast-forward merge,
	// which is the point: the message is the one artefact that leaves the
	// machine. Inert unless a workspace opts in with `undercover = true`.
	{"commit-msg", "commitmsg"},
}

// prunedHooks are hook names this tool prunes but never installs. A re-install
// removes any of these whose on-disk shim is still ours (marker-based) so a
// stranded managed shim stops firing; a foreign hook by that name is left
// untouched. Add a name here to have install prune a managed hook.
var prunedHooks = []string{"pre-push"}

// binShim is a git-hook script that execs the aphrollo binary's tdd subcommand.
// It carries installMarker so a re-install or uninstall recognises its own shim
// and never touches a hand-written hook. The path is slash-normalized and quoted:
// on Windows os.Executable yields `C:\Users\…\aphrollo.exe`, and in an unquoted
// `#!/bin/sh` line the backslashes are escapes — Git Bash execs
// `C:Users…aphrollo.exe`, every gated commit fails "not found". Quoting also
// survives spaces (`C:\Program Files\…`).
// The shim execs the aphrollo binary by ABSOLUTE path, so a path that stops
// resolving — a rename, a half-finished install, a binary deleted while a copy
// of it was running — took every `git` and every `cargo` in every shell down
// with `exec: <path>: not found`, exit 127. Two sessions lost both tools
// entirely for several minutes over exactly that, with a message naming a path
// and no way to act on it.
//
// The gate is best-effort; the tools it wraps are not. A missing binary
// degrades to running the real tool UNGATED, says so once, and names the
// command that fixes it. The real tool's path is resolved at INSTALL time: the
// shim cannot look it up itself, because its own directory sits ahead of the
// real one on PATH by design. With no fallback resolved it says the same thing
// and stops, rather than exec-ing an empty path — which a shell reads as
// running the shim's own directory.
func binShim(bin, sub, fallback string) string {
	missing := "gate: " + shellPath(bin) + " is missing — running " + sub +
		" UNGATED; fix with: aphrollo gate self-install"
	guard := "if [ ! -x \"" + shellPath(bin) + "\" ]; then\n" +
		"  echo \"" + missing + "\" >&2\n"
	if fallback == "" {
		guard += "  exit 127\n"
	} else {
		guard += "  exec \"" + shellPath(fallback) + "\" \"$@\"\n"
	}
	guard += "fi\n"
	return "#!/bin/sh\n" + installMarker + "\n" + guard +
		"exec \"" + shellPath(bin) + "\" " + CmdName + " " + sub + " \"$@\"\n"
}

// shellPath renders a binary path for embedding in a shell command line:
// backslashes become forward slashes UNCONDITIONALLY (filepath.ToSlash is a
// no-op off Windows, but a Windows path must render identically wherever the
// string is generated or tested — same-bytes-out determinism). Windows accepts
// forward slashes natively; a POSIX filename containing a literal backslash is
// not a path this installer ever writes.
func shellPath(bin string) string {
	return strings.ReplaceAll(bin, `\`, "/")
}

// InitGitGate installs (or, with uninstall=true, removes) the global git gate:
// the pre-commit/pre-push shims in hooksDir and git's global core.hooksPath
// pointing at it. One command gates every repo. Foreign hooks are left intact.
// It returns whether anything changed. The git config calls honour
// GIT_CONFIG_GLOBAL, so callers can target a non-default config.
func InitGitGate(hooksDir, bin string, uninstall bool) (bool, error) {
	if uninstall {
		return uninstallGitGate(hooksDir)
	}
	return installGitGate(hooksDir, bin)
}

// HooksDirUnsafeEnv forces installGitGate past the temp/scratchpad refusal
// below, for the one caller that means it: a dogfood run that ALSO points
// GIT_CONFIG_GLOBAL at a throwaway file, never the box's real global config.
const HooksDirUnsafeEnv = "APHROLLO_HOOKS_DIR_UNSAFE"

// unsafeHooksDirReason names why hooksDir is not a durable place for the
// global git gate to live, or "" when it looks durable. core.hooksPath is
// MACHINE-WIDE: a dir under a temp root or a session scratchpad works right
// up until whatever created it cleans up, and git then silently runs NO
// hooks at all — the box stays that way until a human notices and runs
// `git config --global --unset core.hooksPath` by hand.
func unsafeHooksDirReason(hooksDir string) string {
	clean := cleanForCompare(hooksDir)
	for _, root := range temporaryRoots() {
		root = cleanForCompare(root)
		if root == "" {
			continue
		}
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return fmt.Sprintf("it is under the temporary directory %s", root)
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if strings.EqualFold(part, "scratchpad") {
			return "it has a scratchpad path segment"
		}
	}
	return ""
}

// temporaryRoots is every directory this box treats as throwaway: the OS
// default plus the env vars a shell or a session sets to mean "delete this
// later."
func temporaryRoots() []string {
	roots := []string{os.TempDir()}
	for _, name := range []string{"TMPDIR", "TEMP", "TMP"} {
		if v := os.Getenv(name); v != "" {
			roots = append(roots, v)
		}
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		roots = append(roots, filepath.Join(v, "Temp"))
	}
	return roots
}

// cleanForCompare normalizes a path for prefix comparison: absolute, cleaned,
// and lower-cased on Windows where the filesystem is case-insensitive.
func cleanForCompare(p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

// gitGateStage names the appendGateLog stage a dangling-hooksPath reclaim
// records under, so it lands in gate.log and gate stats can count it.
const gitGateStage = "git-gate"

// hooksPathDangling reports whether dir — a current core.hooksPath value —
// has genuinely been DELETED, as opposed to merely unreachable right now. An
// unmounted or not-yet-reconnected mapped network drive returns the exact
// same "not found" error os.Stat gives a deleted directory, and a
// core.hooksPath legitimately pointing at one (a shared `Z:\hooks`) must
// never be silently repointed on the strength of a transient stat failure —
// the depender is fine, and rewriting a machine-wide setting out from under
// it is the same class of mistake the temp/scratchpad refusal above exists
// to prevent, only aimed the other way.
//
// Requiring dir's PARENT to exist and be a readable directory is what tells
// the two apart as far as the platform allows: a directory that was
// genuinely deleted leaves its parent standing, while an unreachable network
// path's parent (often the drive root itself) fails the identical way. A dir
// with no distinct parent (a bare drive root, or ".") has nothing to
// corroborate against and is never called a deletion.
func hooksPathDangling(dir string) bool {
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return false
	}
	fi, err := os.Stat(parent)
	return err == nil && fi.IsDir()
}

func installGitGate(hooksDir, bin string) (bool, error) {
	if reason := unsafeHooksDirReason(hooksDir); reason != "" && os.Getenv(HooksDirUnsafeEnv) != "1" {
		return false, fmt.Errorf(
			"refusing to install the global git gate into %q: %s.\n"+
				"  core.hooksPath is machine-wide; a dir under a temp or scratch location\n"+
				"  gets cleaned up later and leaves every commit on the box silently\n"+
				"  ungated. Pick a durable --git-hooks-dir, or set %s=1 to force it",
			hooksDir, reason, HooksDirUnsafeEnv)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return false, err
	}
	changed := false
	for _, h := range gitGateHooks {
		path := filepath.Join(hooksDir, h.name)
		if foreignHookExists(path) {
			continue // never clobber a hand-written hook
		}
		// A git HOOK has no "real tool" to fall through to: it IS the gate. With
		// the binary gone it says so and stops, which git reports as a failed
		// hook rather than a silently ungated commit.
		want := binShim(bin, h.sub, "")
		if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
			continue
		}
		if err := os.WriteFile(path, []byte(want), 0o755); err != nil {
			return false, err
		}
		changed = true
	}

	// Prune any managed shim for a hook this tool does not install (e.g.
	// pre-push), so a box only runs the managed hooks above.
	pruned, err := prunePrunedHooks(hooksDir)
	if err != nil {
		return false, err
	}
	changed = changed || pruned

	cur, _ := gitConfigGet("core.hooksPath")
	if cur != hooksDir {
		// Never clobber a foreign global core.hooksPath: the user has their own
		// global hooks. We could not restore it on uninstall (we only know our own
		// dir), so refuse and tell them how to proceed. A path we already manage
		// (its shims carry installMarker) is safe to repoint, and so is one that
		// is simply GONE — a dangling hooksPath cannot belong to anyone, since
		// whatever wrote hooks there no longer exists to depend on them.
		if cur != "" && !managedHooksDir(cur) {
			if hooksPathDangling(cur) {
				fmt.Fprintf(os.Stderr,
					"aphrollo gate: hookspath-dangling-repaired — core.hooksPath was %q, which no longer exists; repointing to %s\n",
					cur, hooksDir)
				// Recorded through the same ledger `gate stats` reads, naming
				// the previous value as the root so it can be restored by
				// hand if the reclaim turns out to have been wrong.
				appendGateLog(gitGateStage, cur, "core.hooksPath -> "+hooksDir, "hookspath-dangling-repaired", 0)
			} else {
				return false, fmt.Errorf(
					"refusing to overwrite existing global core.hooksPath %q.\n"+
						"  It points at hooks this tool does not manage. To install the gate, either\n"+
						"  merge your hooks into %s and run again, or unset it first:\n"+
						"    git config --global --unset core.hooksPath",
					cur, hooksDir)
			}
		}
		if err := gitConfigSet("core.hooksPath", hooksDir); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// prunePrunedHooks removes any shim in hooksDir for a hook in prunedHooks whose
// on-disk content is one this tool wrote (carries installMarker). A foreign hook
// by that name — or an absent one — is left untouched. It reports whether it
// removed anything. Shared by install (so the next init cleans up a stranded
// shim) and uninstall.
func prunePrunedHooks(hooksDir string) (bool, error) {
	changed := false
	for _, name := range prunedHooks {
		path := filepath.Join(hooksDir, name)
		if foreignHookExists(path) {
			continue // never remove a hand-written hook
		}
		if _, err := os.Stat(path); err != nil {
			continue // absent: nothing to prune
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// managedHooksDir reports whether dir holds hooks this tool wrote — i.e. its
// pre-commit shim carries installMarker. Used so a re-install can safely repoint
// core.hooksPath from one of our own dirs, while still refusing a foreign one.
func managedHooksDir(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "pre-commit"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), installMarker)
}

func uninstallGitGate(hooksDir string) (bool, error) {
	changed := false
	for _, h := range gitGateHooks {
		path := filepath.Join(hooksDir, h.name)
		if foreignHookExists(path) {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return false, err
			}
			changed = true
		}
	}
	// Also remove any stranded managed shim for a pruned hook (e.g. pre-push).
	pruned, err := prunePrunedHooks(hooksDir)
	if err != nil {
		return false, err
	}
	changed = changed || pruned
	if cur, _ := gitConfigGet("core.hooksPath"); cur == hooksDir {
		if err := gitConfigUnset("core.hooksPath"); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// WriteManagedHookForTest writes a shim this package recognizes as its own
// (carries installMarker) at hooksDir/name, for a doctor fixture in another
// package that needs a "managed hooks dir" WITHOUT going through InitGitGate
// — which would touch the box's real global git config.
func WriteManagedHookForTest(hooksDir, name, bin, sub string) error {
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(hooksDir, name), []byte(binShim(bin, sub, "")), 0o755)
}

// GlobalHooksPath reads the box's current global core.hooksPath, empty when
// unset. Doctor uses it to judge whether the git gate can run at all before
// judging anything the hooks it names would do.
func GlobalHooksPath() string {
	v, _ := gitConfigGet("core.hooksPath")
	return v
}

func gitConfigGet(key string) (string, error) {
	out, err := exec.Command(gitBinary(), "config", "--global", "--get", key).Output()
	return strings.TrimSpace(string(out)), err
}

func gitConfigSet(key, val string) error {
	if out, err := exec.Command(gitBinary(), "config", "--global", key, val).CombinedOutput(); err != nil {
		return fmt.Errorf("git config %s: %v: %s", key, err, out)
	}
	return nil
}

func gitConfigUnset(key string) error {
	if out, err := exec.Command(gitBinary(), "config", "--global", "--unset", key).CombinedOutput(); err != nil {
		return fmt.Errorf("git config --unset %s: %v: %s", key, err, out)
	}
	return nil
}
