package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitGateHooks are the git hooks the gate manages, paired with the `aphrollo
// tdd` subcommand each shim invokes. The gate is mechanical-only: pre-commit is
// the sole managed hook. install also PRUNES any managed pre-push shim it finds
// (see prunedHooks + uninstallGitGate), so a box only runs the hooks listed here.
var gitGateHooks = []struct{ name, sub string }{
	{"pre-commit", "precommit"},
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
func binShim(bin, sub string) string {
	return "#!/bin/sh\n" + installMarker + "\nexec \"" + filepath.ToSlash(bin) + "\" tdd " + sub + " \"$@\"\n"
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

func installGitGate(hooksDir, bin string) (bool, error) {
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return false, err
	}
	changed := false
	for _, h := range gitGateHooks {
		path := filepath.Join(hooksDir, h.name)
		if foreignHookExists(path) {
			continue // never clobber a hand-written hook
		}
		want := binShim(bin, h.sub)
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
		// (its shims carry installMarker) is safe to repoint.
		if cur != "" && !managedHooksDir(cur) {
			return false, fmt.Errorf(
				"refusing to overwrite existing global core.hooksPath %q.\n"+
					"  It points at hooks this tool does not manage. To install the gate, either\n"+
					"  merge your hooks into %s and run again, or unset it first:\n"+
					"    git config --global --unset core.hooksPath",
				cur, hooksDir)
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

func gitConfigGet(key string) (string, error) {
	out, err := exec.Command("git", "config", "--global", "--get", key).Output()
	return strings.TrimSpace(string(out)), err
}

func gitConfigSet(key, val string) error {
	if out, err := exec.Command("git", "config", "--global", key, val).CombinedOutput(); err != nil {
		return fmt.Errorf("git config %s: %v: %s", key, err, out)
	}
	return nil
}

func gitConfigUnset(key string) error {
	if out, err := exec.Command("git", "config", "--global", "--unset", key).CombinedOutput(); err != nil {
		return fmt.Errorf("git config --unset %s: %v: %s", key, err, out)
	}
	return nil
}
