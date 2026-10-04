package mutation

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A repo can keep its Go module below the repo root (backend-go/go.mod beside
// a frontend and a Python service). The rest of the commit gate takes the
// project root of each staged file, the nearest directory with a build file;
// the commit-time mutation stage resolves the Go modules the same way and
// measures each from its own directory, because that is where `go test` finds
// its go.mod and where the package paths it is given resolve.

// commitModules is the Go modules, as repo-relative slash directories with ""
// for the repo root, that own at least one of files: the project root
// FindProjectRoot gives each file, kept when it is a Go module (a root that
// also carries Cargo.toml is a Cargo project, as isGoModuleRepo has it).
func commitModules(repoRoot string, files []string) []string {
	seen := map[string]bool{}
	for _, file := range files {
		root := FindProjectRoot(filepath.Join(repoRoot, filepath.FromSlash(file)))
		if root == "" || !isGoModuleRepo(root) {
			continue
		}
		rel, err := filepath.Rel(repoRoot, root)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		seen[modulePrefix(rel)] = true
	}
	mods := make([]string, 0, len(seen))
	for mod := range seen {
		mods = append(mods, mod)
	}
	sort.Strings(mods)
	return mods
}

// modulePrefix is a module directory as the slash prefix of its paths: ""
// for the repo root, else "backend-go/".
func modulePrefix(rel string) string {
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" {
		return ""
	}
	return rel + "/"
}

// moduleDir is the directory of a module named by its prefix.
func moduleDir(repoRoot, prefix string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(prefix))
}

// inModule restricts the repo-relative added lines and unstaged files to those
// under prefix and rewrites their paths relative to the module.
func inModule(prefix string, added map[string]map[int]bool, unstaged map[string]bool) (map[string]map[int]bool, map[string]bool) {
	subAdded := map[string]map[int]bool{}
	for file, lines := range added {
		if rest, ok := strings.CutPrefix(file, prefix); ok {
			subAdded[rest] = lines
		}
	}
	subUnstaged := map[string]bool{}
	for file := range unstaged {
		if rest, ok := strings.CutPrefix(file, prefix); ok {
			subUnstaged[rest] = true
		}
	}
	return subAdded, subUnstaged
}

// repoRelative puts the module's prefix back on a run's paths, so what the
// verdict names, and what mutation-accept entries match, are the paths the
// commit shows.
func repoRelative(prefix string, runs []commitRun) {
	if prefix == "" {
		return
	}
	for i := range runs {
		r := &runs[i]
		r.Mutant.File = prefix + r.Mutant.File
		r.Outcome.File = prefix + r.Outcome.File
		r.Outcome.Name = mutantLineOf(r.Mutant.File, r.Mutant.Line, r.Mutant.Col, r.Mutant.Mutation)
	}
}

// measureModules measures each module's share of the added lines and answers
// one result: refused when any module's was, with each message in order.
func measureModules(displayName, repoRoot string, mods []string, cfg MutantsConfig,
	added map[string]map[int]bool, unstaged map[string]bool, start time.Time) GateResult {
	var blocked bool
	var messages []string
	for _, prefix := range mods {
		subAdded, subUnstaged := inModule(prefix, added, unstaged)
		res := measureAddedLines(displayName, repoRoot, prefix, cfg, subAdded, subUnstaged, start)
		blocked = blocked || res.Blocked
		if res.Message != "" {
			messages = append(messages, res.Message)
		}
	}
	return mutantsResult(blocked, strings.Join(messages, "\n"))
}
