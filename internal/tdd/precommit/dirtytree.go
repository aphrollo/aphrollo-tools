package precommit

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// rootPlan is what one root's stages build, resolved once for the merge
// gate so its dirty-tree check and its stages read the same answer: a Go
// root's suite runner, narrowed where the staged files allow, or a cargo
// root's stage plan. A root with neither builds nothing the check can name.
type rootPlan struct {
	goRunner *Runner
	cargo    *cargoStagePlan
	cargoOK  bool
}

// planRoots resolves each group's build scope through the same calls its
// stages make (narrowToStaged for Go, planCargoStages for cargo) and keeps
// it on the group. The cargo plan is reused by gateRootCargo rather than
// resolved twice: resolving it reports each unowned file on stderr.
func planRoots(gateName, repoRoot string, groups []rootGroup) {
	for i := range groups {
		g := &groups[i]
		g.plan = &rootPlan{}
		runner, _ := DetectRunner(g.Root)
		files := append(append([]string{}, g.tests...), g.srcs...)
		switch runner.Cmd {
		case "cargo":
			plan, ok := planCargoStages(gateName, repoRoot, g.Root, files)
			g.plan.cargo, g.plan.cargoOK = &plan, ok
		case "go":
			narrowed := narrowedRunner(runner, repoRoot, g.Root, files)
			g.plan.goRunner = &narrowed
		}
	}
}

// narrowedRunner is runner scoped to the staged files' packages and their
// dependents, or runner itself when nothing narrows it.
func narrowedRunner(runner Runner, repoRoot, root string, files []string) Runner {
	if scoped, ok := narrowToStaged(runner, root, toRootRelative(repoRoot, root, files)); ok {
		return scoped
	}
	return runner
}

// dirtyTreeStage refuses a merge, before anything is built, when an
// uncommitted source or test file lies inside a Go package or cargo crate
// the gate is about to build or test (issue #791). The gate builds the
// worktree, and the merge commits the index: such a file decides the
// verdict while the merge never carries it, so a broken work-in-progress
// test reads as the merge's failure and a green vouches for code that is
// not in the commit. A dirty file anywhere else cannot move the verdict and
// is left alone; an ignored file is not part of the tree at all.
func dirtyTreeStage(gateName, repoRoot string, groups []rootGroup) GateResult {
	const stage, cmd = "dirty-tree", "git diff + git ls-files --others --exclude-standard"
	dirty, err := dirtyWorktreeFiles(repoRoot)
	if err != nil {
		return verdictFor(gateName, stage, repoRoot, cmd, stageOutcome{
			Kind:    outcomeCheckError,
			Err:     err,
			Message: fmt.Sprintf("gate %s: cannot list the uncommitted files (%v), so whether one lies inside what this merge builds is unknown. Fix the git failure and run the merge again.", gateName, err),
		})
	}
	paths := make([]string, 0, len(dirty))
	for p := range dirty {
		paths = append(paths, p)
	}
	tests, srcs := splitKinds(paths)
	inside := map[string]bool{}
	for _, g := range groups {
		for _, f := range append(append([]string{}, tests...), srcs...) {
			if g.plan.builds(repoRoot, g.Root, f) {
				inside[f] = true
			}
		}
	}
	if len(inside) == 0 {
		return verdictFor(gateName, stage, repoRoot, cmd, stageOutcome{Kind: outcomePass})
	}
	names := make([]string, 0, len(inside))
	for f := range inside {
		names = append(names, f)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "gate %s: refused before building — %d uncommitted file(s) lie inside the packages this merge builds, so its verdict would be about them rather than the merge:\n", gateName, len(names))
	for _, f := range names {
		fmt.Fprintf(&b, "  %s (%s)\n", f, dirty[f])
	}
	b.WriteString("Commit, stash or move each one out of the tree, then run the merge again. Uncommitted files outside the built packages do not block.")
	return verdictFor(gateName, stage, repoRoot, cmd, stageOutcome{Kind: outcomeFail, Message: b.String()})
}

// builds reports whether f (repo-relative) lies inside a package or crate
// this plan builds or tests. A Go package is judged within its own root; a
// cargo crate within the whole workspace, since a crate the plan builds
// downstream of the touched one is a project root of its own.
func (p *rootPlan) builds(repoRoot, root, f string) bool {
	switch {
	case p.goRunner != nil:
		rel, ok := relBeneath(repoRoot, root, f)
		return ok && goRunnerBuilds(*p.goRunner, goPackageDir(root, path.Dir(rel)))
	case p.cargoOK:
		ws := p.cargo.ws
		rel, ok := relBeneath(repoRoot, ws, f)
		if !ok {
			return false
		}
		built := map[string]bool{}
		for _, c := range append(append([]string{}, p.cargo.downstream...), p.cargo.alwaysRun...) {
			built[c] = true
		}
		// A workspace manifest, lockfile or cargo config in the merge checks
		// the workspace beyond the touched crates: any crate may be compiled.
		wide := len(p.cargo.wsManifestHit) > 0
		for _, owner := range cargoPackagesOwning(ws, []string{rel}) {
			if wide || built[owner] {
				return true
			}
		}
	}
	return false
}

// relBeneath is f (repo-relative) relative to dir, slash-separated, and
// whether f lies beneath dir at all.
func relBeneath(repoRoot, dir, f string) (string, bool) {
	rel, err := filepath.Rel(dir, filepath.Join(repoRoot, f))
	rel = filepath.ToSlash(rel)
	return rel, err == nil && !strings.HasPrefix(rel, "../")
}

// goRunnerBuilds reports whether a `go test` runner's package arguments
// cover the root-relative package dir: a named package exactly, a `/...`
// pattern the dir and everything beneath it.
func goRunnerBuilds(r Runner, dir string) bool {
	for _, a := range r.Args {
		pkg := path.Clean(a)
		if pkg == "..." {
			return true
		}
		if base, ok := strings.CutSuffix(pkg, "/..."); ok && (dir == base || strings.HasPrefix(dir, base+"/")) {
			return true
		}
		if pkg == dir {
			return true
		}
	}
	return false
}

// dirtyWorktreeFiles lists, repo-relative, every worktree file that differs
// from the index and every untracked file git does not ignore, each with
// how it differs.
func dirtyWorktreeFiles(repoRoot string) (map[string]string, error) {
	out := map[string]string{}
	diff, err := git(repoRoot, "diff", "--name-only", "--no-relative", "-z")
	if err != nil {
		return nil, fmt.Errorf("git diff: %v: %s", err, strings.TrimSpace(diff))
	}
	for _, p := range strings.Split(diff, "\x00") {
		if p != "" {
			out[p] = "modified"
		}
	}
	untracked, err := git(repoRoot, "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %v: %s", err, strings.TrimSpace(untracked))
	}
	for _, p := range strings.Split(untracked, "\x00") {
		if p != "" {
			out[p] = "untracked"
		}
	}
	return out, nil
}
