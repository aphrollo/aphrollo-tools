package install

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
	// Prune holds paths of managed shims this install removes — the per-repo
	// mirror of the global gate's pruned hooks. A stranded managed pre-push shim
	// is removed so a repo runs only the hooks Apply writes.
	Prune []string
}

// perRepoHooks are the git hooks per-repo install writes, paired with the
// `aphrollo tdd` subcommand each shim invokes. pre-merge-commit was added
// 2026-08-15 (build-infra-fix task A6) alongside the global gate's — see
// gitGateHooks' doc comment for why.
var perRepoHooks = []struct{ Name, Sub string }{
	{"pre-commit", "precommit"},
	{"pre-merge-commit", "premerge"},
	// One post-commit shim for both halves — the gate note, then the lane's
	// mutation run. See gitGateHooks.
	{"post-commit", "postcommit"},
	{"commit-msg", "commitmsg"},
	// post-merge runs the opt-in lane sweep after a merge git itself made —
	// the sweep `aphrollo workspace merge` has always ended with, now
	// reachable from a plain `git merge`. See gitGateHooks.
	{"post-merge", "postmerge"},
}

// perRepoPrunedHooks are hook names per-repo install removes but never writes. A
// managed shim by one of these names (marker-based) is pruned so a stranded
// shim stops firing; a foreign hook by that name is left untouched.
var perRepoPrunedHooks = []string{"pre-push"}

// shim is the hook script body; bin is the absolute aphrollo binary path and sub
// the matching `aphrollo tdd` subcommand. Using the resolved path (not a bare
// `aphrollo`) means the hook invokes the exact binary that installed it, so it
// works even when aphrollo is not on the hook process's PATH — matching the
// global gate's binShim.
func shim(bin, sub string) string {
	// Slash-normalized + quoted for the same reason as binShim: a raw Windows
	// path's backslashes are sh escapes, so the exec line resolves to garbage.
	// "$@" forwards git's own arguments: commit-msg is handed the message
	// file path, and a shim that swallowed it would gate nothing.
	return "#!/bin/sh\n" + installMarker + "\nexec \"" + shellPath(bin) + "\" " + CmdName + " " + sub + " \"$@\"\n"
}

// BuildInstallPlan computes the hooks to install for the repo at repoRoot, whose
// shims invoke the binary at bin. It returns an error if repoRoot is not a git
// repository.
func BuildInstallPlan(repoRoot, bin string) (InstallPlan, error) {
	hooksDir, err := repoHooksDir(repoRoot)
	if err != nil {
		return InstallPlan{}, err
	}

	plan := InstallPlan{RepoRoot: repoRoot}
	for _, h := range perRepoHooks {
		path := filepath.Join(hooksDir, h.Name)
		plan.Hooks = append(plan.Hooks, HookFile{
			Path:     path,
			Content:  shim(bin, h.Sub),
			Conflict: foreignHookExists(path),
		})
	}
	for _, name := range perRepoPrunedHooks {
		path := filepath.Join(hooksDir, name)
		if foreignHookExists(path) {
			continue // never remove a hand-written hook
		}
		if _, err := os.Stat(path); err == nil {
			plan.Prune = append(plan.Prune, path)
		}
	}
	return plan, nil
}

// repoHooksDir resolves the directory git will actually run repoRoot's hooks
// from. A checkout whose `.git` is a DIRECTORY keeps the direct answer,
// `<root>/.git/hooks`. A LINKED WORKTREE's `.git` is a FILE holding
// `gitdir: <common>/.git/worktrees/<name>`, and its hooks are shared: they
// live in the COMMON git dir every worktree of the repo has. Statting `.git`
// and demanding a directory refused every lane `git worktree add` creates,
// which is the one place the merge-only primary checkout tells you to
// regenerate the managed CLAUDE.md block from (#588).
//
// git answers the linked case itself, via `rev-parse --git-common-dir`, read
// through the STDOUT-ONLY gitRead: git writes warnings to stderr while still
// answering on stdout, and folding the two together (CombinedOutput) makes the
// warning part of the path — a directory `install --apply` then creates, with
// every hook written under it. Deliberately not `--git-path hooks`, which
// HONOURS `core.hooksPath`. That
// setting is global on a box running the gate's own git hooks, so
// `--git-path` would point every per-repo install at the box-wide hooks
// directory and let the prune step delete the global gate's own shims.
//
// A directory with no `.git` at all is not a repository, the same refusal as
// before.
func repoHooksDir(repoRoot string) (string, error) {
	fi, err := os.Stat(filepath.Join(repoRoot, ".git"))
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository (no .git directory or file)", repoRoot)
	}
	if fi.IsDir() {
		return filepath.Join(repoRoot, ".git", "hooks"), nil
	}
	out, gitErr := gitRead(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	common := strings.TrimSpace(out)
	if gitErr != nil || common == "" {
		return "", fmt.Errorf("%s has a .git file (a linked worktree) whose common git directory git could not "+
			"resolve: %v", repoRoot, gitErr)
	}
	return filepath.Join(filepath.FromSlash(common), "hooks"), nil
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
	pruneVerb := "would prune"
	if apply {
		pruneVerb = "pruning"
	}
	for _, path := range p.Prune {
		fmt.Fprintf(&b, "  %s stranded shim %s\n", pruneVerb, path)
	}
	if !apply {
		b.WriteString("run again with --apply to write them.\n")
	}
	return b.String()
}

// Apply writes the non-conflicting hooks, making them executable, and removes
// any stranded managed shim in Prune. Conflicting hooks are left untouched so a
// hand-written hook is never destroyed.
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
	for _, path := range p.Prune {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
