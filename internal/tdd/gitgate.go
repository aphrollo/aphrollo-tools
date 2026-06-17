package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitGateHooks are the two git hooks the gate manages, paired with the
// `aphrollo tdd` subcommand each shim invokes.
var gitGateHooks = []struct{ name, sub string }{
	{"pre-commit", "precommit"},
	{"pre-push", "prepush"},
}

// binShim is a git-hook script that execs the aphrollo binary's tdd subcommand.
// It carries installMarker so a re-install or uninstall recognises its own shim
// and never touches a hand-written hook.
func binShim(bin, sub string) string {
	return "#!/bin/sh\n" + installMarker + "\nexec " + bin + " tdd " + sub + " \"$@\"\n"
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
