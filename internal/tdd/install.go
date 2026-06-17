package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// installMarker identifies a hook this tool wrote, so a re-install can safely
// overwrite its own shim while never clobbering a hand-written hook.
const installMarker = "# aphrollo-tdd managed hook"

// HookFile is one git hook the installer would write.
type HookFile struct {
	Path     string
	Content  string
	Conflict bool // a non-aphrollo hook already exists here — do not overwrite
}

// InstallPlan is the set of git hooks `aphrollo tdd install` would write into a
// single repository. Installation is deliberately PER-REPO: it writes into the
// repo's own .git/hooks rather than setting a global core.hooksPath, so the
// gates only fire in repos that opted in (the original installed globally and
// fired in every repo on the machine).
type InstallPlan struct {
	RepoRoot string
	Hooks    []HookFile
}

// shim is the hook script body; bin is the absolute aphrollo binary path and sub
// the matching `aphrollo tdd` subcommand. Using the resolved path (not a bare
// `aphrollo`) means the hook invokes the exact binary that installed it, so it
// works even when aphrollo is not on the hook process's PATH — matching the
// global gate's binShim.
func shim(bin, sub string) string {
	return "#!/bin/sh\n" + installMarker + "\nexec " + bin + " tdd " + sub + "\n"
}

// BuildInstallPlan computes the hooks to install for the repo at repoRoot, whose
// shims invoke the binary at bin. It returns an error if repoRoot is not a git
// repository.
func BuildInstallPlan(repoRoot, bin string) (InstallPlan, error) {
	hooksDir := filepath.Join(repoRoot, ".git", "hooks")
	if fi, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil || !fi.IsDir() {
		return InstallPlan{}, fmt.Errorf("%s is not a git repository (no .git directory)", repoRoot)
	}

	plan := InstallPlan{RepoRoot: repoRoot}
	for _, h := range []struct{ name, sub string }{
		{"pre-commit", "precommit"},
		{"pre-push", "prepush"},
	} {
		path := filepath.Join(hooksDir, h.name)
		plan.Hooks = append(plan.Hooks, HookFile{
			Path:     path,
			Content:  shim(bin, h.sub),
			Conflict: foreignHookExists(path),
		})
	}
	return plan, nil
}

// foreignHookExists reports whether path holds a hook this tool did NOT write.
func foreignHookExists(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return !strings.Contains(string(data), installMarker)
}

// Render describes the plan. When apply is false it is a dry-run preview.
func (p InstallPlan) Render(apply bool) string {
	var b strings.Builder
	verb := "would install"
	if apply {
		verb = "installing"
	}
	fmt.Fprintf(&b, "%s aphrollo tdd git hooks in %s:\n", verb, p.RepoRoot)
	for _, h := range p.Hooks {
		if h.Conflict {
			fmt.Fprintf(&b, "  SKIP %s (a non-aphrollo hook already exists — merge by hand)\n", h.Path)
			continue
		}
		fmt.Fprintf(&b, "  %s\n", h.Path)
	}
	if !apply {
		b.WriteString("run again with --apply to write them.\n")
	}
	return b.String()
}

// Apply writes the non-conflicting hooks, making them executable. Conflicting
// hooks are left untouched so a hand-written hook is never destroyed.
func (p InstallPlan) Apply() error {
	for _, h := range p.Hooks {
		if h.Conflict {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(h.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(h.Path, []byte(h.Content), 0o755); err != nil {
			return err
		}
	}
	return nil
}
