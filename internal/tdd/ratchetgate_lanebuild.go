package tdd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The fixtures stage judged a tree with the binary that happened to be
// installed, which a lane correcting a MATCHER cannot use: its new fixture
// rows are by construction rows the installed binary must reject, and that
// rejection is what makes them a fix. So the lane could neither commit nor
// merge until the box binary already carried the lane's own change, and the
// only way out was to swap a machine-wide binary to unmerged code — done
// twice on lane/marker-code, which is what issues #659 and #673 are.
//
// The stage now splits the run between two judges. Laws whose `.toml` or
// whose fixtures THIS COMMIT stages go to a binary built from the checkout
// under judgement; every other law stays with the installed binary. The
// split is exact — RunFixturesWith's Except half and the lane's Only half
// together cover each law once — so no law goes unjudged and no law is
// judged twice.
//
// The bound is the whole safety argument, and it is enforced on both sides.
// The lane is only ever ASKED about the laws it staged, and a verdict it
// returns for any other law is DISCARDED rather than counted: a lane grades
// its own homework only for the homework it actually changed, and a lane
// build that lied about a law it never touched changes nothing, because the
// installed binary judged that law anyway.
//
// A repo that does not COMPILE the matchers has no lane build to run — a
// consuming repo's laws are data, and the installed binary is the only judge
// there is — so it never pays for a build.

// lawEngineModule is the module path of the checkout that compiles the
// matchers. A lane build is only meaningful there.
const lawEngineModule = "github.com/aphrollo/aphrollo-tools"

// laneBuildTimeout bounds the lane's own build. A build that hangs must
// refuse the commit with a reason rather than hold the gate open forever;
// the whole build is seconds on a warm cache, so this is a hang detector,
// not a budget.
const laneBuildTimeout = 5 * time.Minute

// laneFixtureBinName always carries .exe: Windows will not execute an
// extensionless file, and on unix the name of a file has no bearing on
// whether it runs, so one spelling works on both hosts without reading the
// OS here.
const laneFixtureBinName = "aphrollo-lane.exe"

// laneBuildFixHint is what a session does about a refusal from this path:
// the lane's build IS the judge, so a build that fails is a lane that
// cannot state its own law, not a gate to work around.
const laneBuildFixHint = "the laws this commit changes are judged by a build of this checkout — fix the build, or unstage the law and fixture changes"

// ModulePath reports the module path root's go.mod declares. go.mod permits
// blank lines and `//` comments before the module directive, so those are
// skipped before the first real line is read; anything else on that line
// means this is not a module whose path can be named.
func ModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		path, ok := strings.CutPrefix(trimmed, "module ")
		if !ok {
			return "", fmt.Errorf("%s/go.mod declares no module (first directive is %q)", root, trimmed)
		}
		return strings.TrimSpace(path), nil
	}
	return "", fmt.Errorf("%s/go.mod declares no module", root)
}

// IsLawEngineCheckout reports whether root is a checkout of the module that
// compiles the law engine, with the command to build. Only there can a
// lane's own build differ from the installed binary's judgement.
func IsLawEngineCheckout(root string) bool {
	path, err := ModulePath(root)
	if err != nil || path != lawEngineModule {
		return false
	}
	return dirExists(filepath.Join(root, "cmd", "aphrollo"))
}

// dirExists reports whether path is a directory that can be read.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// laneChangedLaws names the laws THIS COMMIT stages a change to: the law's
// own `.toml`, or any file under its fixtures. This is the bound on what a
// lane may answer for, so it reads the index and nothing else — a law the
// working tree happens to differ on is not a law this commit changes.
func laneChangedLaws(repoRoot string) []string {
	named := map[string]bool{}
	for _, f := range stagedFiles(repoRoot) {
		rel := filepath.ToSlash(f)
		if law, ok := strings.CutPrefix(rel, ratchet.LawsDir+"/"); ok {
			if name, isLaw := strings.CutSuffix(law, ".toml"); isLaw && !strings.Contains(name, "/") {
				named[name] = true
			}
			continue
		}
		if law, ok := fixtureLawFromPath(rel); ok {
			named[law] = true
		}
	}
	out := make([]string, 0, len(named))
	for name := range named {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// laneJudgedLaws is laneChangedLaws intersected with the laws the tree
// actually has, in a checkout that can build a judge at all. A commit that
// DELETES a law stages a path naming a law nobody can judge any more, and
// demanding a verdict for it would refuse every law-deleting commit.
func laneJudgedLaws(repoRoot string) []string {
	if !IsLawEngineCheckout(repoRoot) {
		return nil
	}
	changed := laneChangedLaws(repoRoot)
	if len(changed) == 0 {
		return nil
	}
	// A load error here is reported by the fixtures run itself, which is
	// about to fail on the same laws with the same error and a remedy
	// attached; answering "no lane laws" only routes them to that report.
	laws, err := ratchet.LoadLaws(repoRoot)
	if err != nil {
		return nil
	}
	present := map[string]bool{}
	for _, law := range laws {
		present[law.Name] = true
	}
	var out []string
	for _, name := range changed {
		if present[name] {
			out = append(out, name)
		}
	}
	return out
}

// laneFixtureBuild compiles the checkout under judgement into a temporary
// directory, returning the binary and the cleanup that removes it. A var so
// the stage's decision can be tested without a toolchain run.
//
// -buildvcs=false for the reason `gate self-install` and `aphrollo update`
// use it: this runs from a linked worktree with a dirty index, where
// stamping VCS info either fails or embeds the wrong revision.
var laneFixtureBuild = func(root string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "aphrollo-lane-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	bin := filepath.Join(dir, laneFixtureBinName)
	ctx, cancel := context.WithTimeout(context.Background(), laneBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", bin, "./cmd/aphrollo")
	cmd.Dir = root
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		said := strings.TrimSpace(errb.String())
		if said == "" {
			said = err.Error()
		}
		return "", cleanup, fmt.Errorf("go build -o %s ./cmd/aphrollo in %s: %s", bin, root, said)
	}
	return bin, cleanup, nil
}

// laneFixtureRun asks the lane's own build for exactly the named laws'
// fixture verdicts, as data. A non-zero exit with parseable output is an
// ordinary failing verdict the stage must report law by law, not a tooling
// failure; only output that is not a verdict list at all is one.
var laneFixtureRun = func(bin, root string, laws []string) ([]ratchet.FixtureResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), laneBuildTimeout)
	defer cancel()
	argv := []string{"ratchet", "test", "--repo", root, "--only", strings.Join(laws, ","), "--format", "json"}
	cmd := exec.CommandContext(ctx, bin, argv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	var results []ratchet.FixtureResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		said := strings.TrimSpace(errb.String())
		if runErr != nil {
			return nil, fmt.Errorf("%s %s: %v\n%s", bin, strings.Join(argv, " "), runErr, said)
		}
		return nil, fmt.Errorf("%s %s: output is not a fixture verdict list: %v\n%s",
			bin, strings.Join(argv, " "), err, said)
	}
	return results, nil
}

// laneJudgedFixtures builds the checkout under judgement and asks it for the
// verdicts on the laws this commit stages, announcing the split so a session
// reading the gate's output can see which judge answered for what.
func laneJudgedFixtures(gateName, repoRoot string, laws []string) ([]ratchet.FixtureResult, error) {
	fmt.Fprintf(os.Stderr,
		"gate %s: ratchet fixtures: %d law(s) this commit changes are judged by a build of this checkout: %s\n",
		gateName, len(laws), strings.Join(laws, ", "))
	bin, cleanup, err := laneFixtureBuild(repoRoot)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	raw, err := laneFixtureRun(bin, repoRoot, laws)
	if err != nil {
		return nil, err
	}
	return boundToStagedLaws(raw, laws)
}

// boundToStagedLaws is the self-grading bound, enforced on what comes BACK:
// a verdict for a law this commit did not stage is discarded, so a lane
// cannot answer for ground it did not touch even if its build says it can —
// and a law it was asked about and did not answer for is an error, because
// an absent verdict is not a clean one.
func boundToStagedLaws(raw []ratchet.FixtureResult, laws []string) ([]ratchet.FixtureResult, error) {
	asked := make(map[string]bool, len(laws))
	for _, law := range laws {
		asked[law] = true
	}
	var kept []ratchet.FixtureResult
	answered := map[string]bool{}
	for _, r := range raw {
		if !asked[r.Law] || answered[r.Law] {
			continue
		}
		answered[r.Law] = true
		kept = append(kept, r)
	}
	var missing []string
	for _, law := range laws {
		if !answered[law] {
			missing = append(missing, law)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("this checkout's own build returned no fixture verdict for %s",
			strings.Join(missing, ", "))
	}
	return kept, nil
}
