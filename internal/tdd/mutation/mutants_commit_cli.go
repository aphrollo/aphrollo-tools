package mutation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
	cmd := exec.CommandContext(ctx, "go", "list", "-f", `{{if or .TestGoFiles .XTestGoFiles}}{{.Dir}}{{end}}`, "./...")
	cmd.Dir = root
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
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
		if err != nil || !filepath.IsLocal(rel) {
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
	if err != nil || !cfg.AtCommit || !isGoModuleRepo(root) {
		return 0
	}
	if why := commitHeadroomFn(root, 0); why != "" {
		fmt.Fprintf(stdout, "test maps not built: %s\n", why)
		return 0
	}
	ctx := context.Background()
	if len(dirs) == 0 {
		listing, err := goTestedPackagesFn(ctx, root)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo gate mutants testmap: listing the packages with tests: %v\n", err)
			return 1
		}
		dirs = parseTestedDirs(root, listing)
	}
	workers, _ := mutantsJobsForThisBoxFn(mutantsGoJobGB)
	start := time.Now()
	built, fresh, err := refreshTestMaps(ctx, root, cfg, dirs, workers, stdout)
	fmt.Fprintf(stdout, "test maps: %d built, %d current in %s\n", built, fresh, time.Since(start).Round(100*time.Millisecond))
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants testmap: %v\n", err)
		return 1
	}
	return 0
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
