package mutation

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// The two verbs the commit-time run adds: `gate mutants commit`, which runs
// the commit stage by hand on the staged change, and `gate mutants testmap`,
// which builds or refreshes the per-function test maps. The map build is what
// the post-merge hook starts in the background, so it is inert in a repo that
// never declared mutants-at-commit.

// goTestedPackagesFn lists the module's packages that have tests, one
// directory per line. A seam so a test does not need a toolchain.
var goTestedPackagesFn = listTestedPackages

// listTestedPackages is the real listing.
func listTestedPackages(ctx context.Context, root string) (string, error) {
	var out, errOut bytes.Buffer
	spec := run.Spec{Name: "go", Args: []string{"list", "-f", `{{if or .TestGoFiles .XTestGoFiles}}{{.Dir}}{{end}}`, "./..."}, Dir: root, Stdout: &out, Stderr: &errOut}
	if err := run.LightRunCtx(ctx, spec); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// parseTestedDirs turns that listing into the repo-relative, slash-separated,
// sorted directories inside root, without repeats. The root package is ".".
func parseTestedDirs(root, listing string) []string {
	var dirs []string
	for line := range strings.SplitSeq(listing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil || !filepath.IsLocal(rel) || isMutationData(rel+"/") {
			continue
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// RunMutantsTestMap is `gate mutants testmap`: build the per-function test
// map of each package named, or of every package with tests, whose kept map is
// not the tree's. A repo that does not declare mutants-at-commit, or a box
// with no memory to spare, is left alone with exit 0, since a hook runs this
// after every merge.
func RunMutantsTestMap(root string, dirs []string, stdout, stderr io.Writer) int {
	cfg, err := ReadMutantsConfig(root)
	if err != nil || !cfg.AtCommit {
		return 0
	}
	mods := trackedGoModules(root)
	if len(mods) == 0 {
		return 0
	}
	if why := commitHeadroomFn(root, 0); why != "" {
		fmt.Fprintf(stdout, "test maps not built: %s\n", why)
		return 0
	}
	code := 0
	for _, prefix := range mods {
		var named []string
		if len(dirs) > 0 {
			named = namedUnder(prefix, dirs)
			if len(named) == 0 {
				continue
			}
		}
		if testMapModule(root, prefix, cfg, named, stdout, stderr) != 0 {
			code = 1
		}
	}
	return code
}

// testMapModule builds the test maps of the module at prefix below root; dirs
// are relative to the module, and none means every package with tests.
func testMapModule(root, prefix string, cfg MutantsConfig, dirs []string, stdout, stderr io.Writer) int {
	modRoot := moduleDir(root, prefix)
	ctx := context.Background()
	if len(dirs) == 0 {
		listing, err := goTestedPackagesFn(ctx, modRoot)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo gate mutants testmap: listing the packages with tests: %v\n", err)
			return 1
		}
		dirs = parseTestedDirs(modRoot, listing)
	}
	workers, _ := mutantsJobsForThisBoxFn(mutantsGoJobGB)
	start := commitNowFn()
	built, fresh, err := refreshTestMaps(ctx, modRoot, cfg, dirs, workers, stdout)
	label := ""
	if prefix != "" {
		label = " in " + strings.TrimSuffix(prefix, "/") + ","
	}
	fmt.Fprintf(stdout, "test maps:%s %d built, %d current in %s\n", label, built, fresh, commitNowFn().Sub(start).Round(100*time.Millisecond))
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants testmap: %v\n", err)
		return 1
	}
	return 0
}

// trackedGoModules is the Go modules a repo's testmap verb builds for: the
// repo root when it is one, else every directory below it with a go.mod of
// its own that git tracks, as module prefixes (see commitModules).
func trackedGoModules(root string) []string {
	if isGoModuleRepo(root) {
		return []string{""}
	}
	out, _, err := gitDiffOutFn(root, "ls-files", "--", ":(glob)**/go.mod")
	if err != nil {
		return nil
	}
	var mods []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isMutationData(line) || strings.Contains("/"+line, "/vendor/") {
			continue
		}
		dir := path.Dir(line)
		if isGoModuleRepo(filepath.Join(root, filepath.FromSlash(dir))) {
			mods = append(mods, dir+"/")
		}
	}
	sort.Strings(mods)
	return mods
}

// namedUnder is the directories among dirs, relative to root, that lie under
// the module at prefix, relative to the module.
func namedUnder(prefix string, dirs []string) []string {
	var named []string
	for _, dir := range dirs {
		if prefix == "" {
			named = append(named, dir)
		} else if rest, ok := strings.CutPrefix(filepath.ToSlash(dir)+"/", prefix); ok {
			named = append(named, cmp.Or(strings.TrimSuffix(rest, "/"), "."))
		}
	}
	return named
}

// RunMutantsCommit is `gate mutants commit`: the commit stage, run by hand on
// the staged change of the checkout at root, printing what the gate prints on
// stderr. The exit code is 1 when a commit would be refused.
func RunMutantsCommit(root string, _, stderr io.Writer) int {
	cfg, err := ReadMutantsConfig(root)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants commit: %v\n", err)
		return 1
	}
	if !cfg.AtCommit {
		fmt.Fprintln(stderr, "aphrollo gate mutants commit: this repo declares no mutants-at-commit, so the commit gate measures nothing")
		return 0
	}
	if mutantsAtCommitStage("commit", root).Blocked {
		return 1
	}
	return 0
}
