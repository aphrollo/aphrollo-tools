package postedit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// The commit gate refuses a commit over a golangci-lint finding, and the
// finding is knowable the moment the file is written (issue #1006, the same
// pattern as the gofmt note). lintEdited runs golangci-lint's fast linters
// over the edited file's package, limited to the lines the edit changed
// against HEAD (--new-from-patch), and returns the findings in the edited
// file for the edit's gate line. It never blocks and never fails the hook.
//
// It runs in the hook, so it keeps to the fast set; the linters that need the
// package type-checked run detached instead (lintdeferred.go) and report at
// the next hook. Measured on this
// repo with the box at a load of about 10 on 8 cores, the fast set over one
// edited package takes about 0.45 s once the analysis cache is warm, against
// about 2.7 s for the whole standard set, which stays the commit gate's job.
// A run past lintEditBudget is named on the gate line and backed off
// (lintEditBackoff), so a cold cache costs one timeout, not one per edit.
const (
	lintEditBudget  = 3 * time.Second
	lintEditBackoff = 10 * time.Minute
	// lintEditShown is how many findings the note names before it counts the
	// rest.
	lintEditShown = 5
	// lintEditLoadPerCore is the runnable load per core at which the box is
	// too busy for the hook to add a lint.
	lintEditLoadPerCore = 2
)

// lintEditContention is golangci-lint's own refusal when another instance
// holds its machine-wide lock: contention, not a finding.
const lintEditContention = "parallel golangci-lint is running"

// lintEditFinding reads one finding line, "path:line:col: message (linter)".
var lintEditFinding = regexp.MustCompile(`^(\S.*?\.go):\d+:\d+: .+$`)

// The seams of the edit-time lint: whether the linter is installed, the
// box's load, and the run itself, so a test states each without the box's.
var (
	lintEditLook = golangciLintOnPath
	lintEditLoad = readLoadAvg
	lintEditRun  = runLintEdit
)

// golangciLintOnPath reports whether golangci-lint is installed.
func golangciLintOnPath() bool {
	_, err := exec.LookPath("golangci-lint")
	return err == nil
}

// lintEdited is the gate-line note for the lint findings an edit to target
// leaves in it; "" when there are none, and whenever the lint did not run.
func lintEdited(target string) string {
	return lintEditedInto(target, nil)
}

// lintEditedInto is lintEdited that also hands every finding it ran into
// *known (not just the few the note names), when known is not nil, so the
// deferred run of the full set can leave them unsaid.
func lintEditedInto(target string, known *[]string) string {
	return lintEditedFiles([]string{target}, known)
}

// lintPackage is the Go files of one package, as paths relative to the repo
// root that holds them.
type lintPackage struct {
	root string
	dir  string
	rels []string
}

// lintPackages groups the Go files among targets by the package directory
// they sit in, in the order the packages first appear.
func lintPackages(targets []string) []lintPackage {
	var pkgs []lintPackage
	for _, target := range targets {
		if !strings.HasSuffix(target, ".go") {
			continue
		}
		root := repoRootNear(filepath.Dir(target))
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, target)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		dir := path.Dir(rel)
		at := slices.IndexFunc(pkgs, func(p lintPackage) bool { return p.root == root && p.dir == dir })
		if at < 0 {
			pkgs = append(pkgs, lintPackage{root: root, dir: dir})
			at = len(pkgs) - 1
		}
		pkgs[at].rels = append(pkgs[at].rels, rel)
	}
	return pkgs
}

// lintEditedFiles is the gate-line note for the lint findings an edit leaves
// in every one of the Go files among targets, one linter run per package;
// "" when there are none, and whenever the lint did not run. known, when not
// nil, gets every finding there was.
func lintEditedFiles(targets []string, known *[]string) string {
	var all []string
	for _, pkg := range lintPackages(targets) {
		findings, note := lintPackageEdits(pkg)
		if note != "" {
			return note
		}
		all = append(all, findings...)
	}
	if len(all) == 0 {
		return ""
	}
	if known != nil {
		*known = all
	}
	return "golangci-lint: " + namedFindings(all)
}

// lintPackageEdits runs the fast linters over one package, limited to the
// lines its files changed against HEAD. It answers the findings in those
// files, or a note when the run outlived its budget.
func lintPackageEdits(pkg lintPackage) (findings []string, note string) {
	root := pkg.root
	if !lintEditLook() || lintBoxLoaded() || lintBackedOff(root) {
		return nil, ""
	}
	var patch strings.Builder
	var changed []string
	for _, rel := range pkg.rels {
		if p := editedLinesPatch(root, rel); p != "" {
			patch.WriteString(p)
			changed = append(changed, rel)
		}
	}
	if len(changed) == 0 {
		return nil, ""
	}
	file, err := os.CreateTemp("", "aphrollo-lint-*.patch")
	if err != nil {
		return nil, ""
	}
	defer os.Remove(file.Name())
	_, werr := file.WriteString(patch.String())
	if cerr := file.Close(); werr != nil || cerr != nil {
		return nil, ""
	}
	// Never waits: a lint another gate step holds the box-wide lock for is a
	// busy box, and the commit gate lints anyway.
	release, ok := TryAcquireLintLock("golangci-lint "+changed[0], root)
	if !ok {
		return nil, ""
	}
	defer release()

	target := "."
	if pkg.dir != "." {
		target = "./" + pkg.dir
	}
	started := time.Now()
	out, timedOut := lintEditRun(root, []string{
		"run", "--fast-only", "--new-from-patch=" + file.Name(),
		"--output.text.print-issued-lines=false", "--output.text.colors=false", "--show-stats=false", target,
	})
	if timedOut {
		markLintBackoff(root)
		AppendGateLog("postedit", root, LogToken(changed[0]), "lint-timeout", time.Since(started))
		return nil, "golangci-lint did not finish in " + lintEditBudget.String() + "; skipped for " + lintEditBackoff.String()
	}
	if strings.Contains(out, lintEditContention) {
		return nil, ""
	}
	for _, rel := range changed {
		findings = append(findings, findingsIn(out, rel)...)
	}
	if len(findings) > 0 {
		AppendGateLog("postedit", root, LogToken(changed[0]), fmt.Sprintf("lint-findings:%d", len(findings)), time.Since(started))
	}
	return findings, ""
}

// namedFindings names the first lintEditShown findings and counts the rest.
func namedFindings(findings []string) string {
	more := ""
	if len(findings) > lintEditShown {
		more = fmt.Sprintf(" and %d more", len(findings)-lintEditShown)
		findings = findings[:lintEditShown]
	}
	return strings.Join(findings, "; ") + more
}

// findingsIn is the finding lines of out that name rel, in order.
func findingsIn(out, rel string) []string {
	var found []string
	for line := range strings.SplitSeq(out, "\n") {
		m := lintEditFinding.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m != nil && strings.TrimPrefix(filepath.ToSlash(m[1]), "./") == rel {
			found = append(found, strings.TrimRight(line, "\r"))
		}
	}
	return found
}

// editedLinesPatch is the unified diff, zero context, of rel against HEAD,
// which --new-from-patch reads to keep only the findings on changed lines. A
// file git does not track yet is one hunk of all its lines. "" when there is
// nothing changed to lint.
func editedLinesPatch(root, rel string) string {
	if diff := gitOut(root, "diff", "-U0", "HEAD", "--", rel); diff != "" {
		return diff + "\n"
	}
	if gitOut(root, "ls-files", "--others", "--exclude-standard", "--", rel) == "" {
		return ""
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || len(src) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", rel, rel, rel, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// runLintEdit runs golangci-lint with args in root within lintEditBudget and
// returns what it printed; timedOut is true when it was cut off.
func runLintEdit(root string, args []string) (out string, timedOut bool) {
	return runLintWithin(root, args, lintEditBudget)
}

// runLintWithin is runLintEdit with the budget stated.
func runLintWithin(root string, args []string, budget time.Duration) (out string, timedOut bool) {
	// A finding exits non-zero; what matters is the text.
	var text bytes.Buffer
	child, err := run.StartHeavy(context.Background(), run.Spec{Name: "golangci-lint", Args: args, Dir: root, EnvAsIs: true, Stdout: &text, Stderr: &text, Timeout: budget})
	if err != nil {
		return text.String(), false
	}
	timedOut = errors.Is(child.Wait(), run.ErrTimeout)
	return text.String(), timedOut
}

// lintBoxLoaded reports whether the box's runnable load is at or past
// lintEditLoadPerCore per core. An unreadable load is not loaded.
func lintBoxLoaded() bool {
	load, cores, ok := lintEditLoad()
	return ok && load >= lintEditLoadPerCore*float64(cores)
}

// readLoadAvg is the box's one-minute load average and core count, from
// /proc/loadavg; not ok where there is none.
func readLoadAvg() (load float64, cores int, ok bool) {
	return loadFromProc(func() ([]byte, error) { return os.ReadFile("/proc/loadavg") })
}

// loadFromProc reads the one-minute load average from the text read returns.
func loadFromProc(read func() ([]byte, error)) (load float64, cores int, ok bool) {
	data, err := read()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, 0, false
	}
	load, err = strconv.ParseFloat(fields[0], 64)
	return load, runtime.NumCPU(), err == nil
}

// lintBackoffMarker is the file whose mtime says when a lint in root last
// ran past its budget.
func lintBackoffMarker(root string) string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "lint-backoff-"+hex.EncodeToString(sum[:6]))
}

// lintBackedOff reports whether a lint in root ran past its budget within
// lintEditBackoff.
func lintBackedOff(root string) bool {
	marker := lintBackoffMarker(root)
	if marker == "" {
		return false
	}
	info, err := os.Stat(marker)
	return err == nil && withinBackoff(time.Since(info.ModTime()))
}

// withinBackoff reports whether a lint that ran past its budget age ago still
// holds the edit hook off.
func withinBackoff(age time.Duration) bool {
	return age < lintEditBackoff
}

// markLintBackoff records that a lint in root ran past its budget just now.
func markLintBackoff(root string) {
	if marker := lintBackoffMarker(root); marker != "" {
		// A backoff that cannot be recorded costs the next edit one more try.
		_ = os.MkdirAll(filepath.Dir(marker), 0o700)
		_ = os.WriteFile(marker, nil, 0o600)
	}
}
