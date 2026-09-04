package tdd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The Rust runner is the consuming repo's own script. The Go one is here,
// because a Go repo has no script to write it in and the receipt has to be the
// same document either way.
//
// The tool is gremlins, and the choice was measured rather than argued:
// go-mutesting does not BUILD on this box (its osutil dependency uses
// syscall.Dup and RLIMIT_NOFILE, neither of which exists on Windows), so its
// wall time is not a number that exists. gremlins builds, scopes to a diff
// (--diff), writes machine-readable results (--output) and takes a worker cap
// (--workers) — the three things this design needs from a mutation tool. On
// internal/tdd its analysis pass found 1626 runnable mutants (85.76% mutator
// coverage) in 2.6 s, behind one full coverage run of the module.

// gremlinsBin is the tool this runner drives. Resolved from PATH: a box
// without it gets a receipt that says so rather than a silent pass.
const gremlinsBin = "gremlins"

// gremlinsArgv is one diff-scoped run: mutate only what the lane changed,
// write the machine-readable report, and stay inside the worker cap. gremlins
// re-runs the package's tests once per mutant, so an uncapped run owns the box
// for as long as it takes.
//
// excludeFiles is repo-relative paths the run must not even WALK — gremlins'
// own `--exclude-files` takes a filepath regexp (exclusion.Rules in internal/exclusion,
// matched against the fs.WalkDir path gremlins mutates from), so each one is
// anchored and escaped into `^<path>$` before being passed. This is the CI
// runner's incremental lever (issue #143): a file whose blob and package
// fence are unchanged since the last measured push is excluded here rather
// than re-mutated, while `--diff` keeps scoping which LINES are mutable
// within whatever gremlins does walk. gremlins takes exactly one positional
// path (cobra.MaximumNArgs(1)) — "./..." makes it walk nothing, report no
// results and exit 0 — so narrowing happens through exclusion, never through
// a second positional argument.
func gremlinsArgv(baseSHA, outPath string, workers int, excludeFiles []string) []string {
	if workers < 1 {
		workers = 1
	}
	argv := []string{"unleash", "--silent",
		"--diff", baseSHA,
		"--output", outPath,
		"--workers", strconv.Itoa(workers),
	}
	for _, f := range excludeFiles {
		argv = append(argv, "--exclude-files", "^"+regexp.QuoteMeta(filepath.ToSlash(f))+"$")
	}
	// A PATH, not a package pattern: gremlins walks the tree from here.
	return append(argv, ".")
}

// gremlinsFileReport is the shape gremlins writes with --output, captured from
// a real run (testdata/gremlins_report.json). Its own totals are NOT read: the
// captured run reported 0 for every total while listing three mutants, so the
// counts here come from the list itself.
type gremlinsFileReport struct {
	FileName  string `json:"file_name"`
	Mutations []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"mutations"`
}

// parseGremlinsReport reads one run's mutants out of its report.
func parseGremlinsReport(data []byte) ([]MutantOutcome, error) {
	var report struct {
		Files []gremlinsFileReport `json:"files"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	var out []MutantOutcome
	for _, f := range report.Files {
		for _, m := range f.Mutations {
			if gremlinsStatus(m.Status) == gremlinsSkipped {
				continue
			}
			file := filepath.ToSlash(f.FileName)
			out = append(out, MutantOutcome{
				File:     file,
				Line:     m.Line,
				Col:      m.Column,
				Mutation: m.Type,
				Name:     mutantLineOf(file, m.Line, m.Column, m.Type),
				Status:   gremlinsStatus(m.Status),
			})
		}
	}
	sortOutcomes(out)
	return out, nil
}

// gremlinsStatus maps gremlins' vocabulary onto the receipt's. NOT COVERED is
// a MISS: "no test runs this code at all" is the strongest version of the
// thing a survivor reports. Anything unrecognised is unviable — never caught,
// because a gate that reads an unknown status as a pass is not a gate.
func gremlinsStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "KILLED":
		return "caught"
	case "LIVED":
		return "missed"
	case "NOT COVERED":
		return gremlinsNotCovered
	case "TIMED OUT":
		return "timeout"
	case "SKIPPED":
		return gremlinsSkipped
	default:
		return "unviable"
	}
}

// gremlinsNotCovered is a mutant gremlins never ran a test for, because its
// coverage profile had no block at the mutant's position. That is NOT the
// claim a survivor makes ("a test ran and did not notice") and must not refuse
// a merge on its own.
//
// The mapping is not reliable enough to read as "no test covers this". Go's
// coverage blocks split at a closure, so for
//
//	return strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
//
// gremlins calls the two mutants inside the closure RUNNABLE and the outer
// `< 0` after it NOT COVERED, although that comparison plainly executes and a
// hand-mutation of it fails a test. On Windows the mapping fails wholesale:
// 4890 NOT COVERED, mutator coverage 0.00%.
//
// So it is counted and reported, never silently dropped, and never confused
// with a measured survivor.
const gremlinsNotCovered = "notcovered"

// gremlinsSkipped is the report's word for a mutant the run's own --diff scope
// left out. It is not an outcome: the report lists every mutant the ANALYSIS
// found, and on a lane diff that is thousands of them against a handful the
// run actually measured.
const gremlinsSkipped = "skipped"

// RunGoMutantsJob is the Go half of the detached job: run gremlins over the
// lane's diff in the warm worktree, then write and sign the same receipt a
// Rust run writes.
func RunGoMutantsJob(jobPath string) int {
	lowerOwnPriority()
	j, ok := readMutantsJob(jobPath)
	if !ok {
		return 0
	}
	// Never in a linked worktree: a mutated gate test rewrites whatever
	// repository it lands in, and a linked worktree's is the lane's own
	// (issue #156). Everything downstream — the run, the dirty check, the
	// accept-list — reads the tree the run actually happened in.
	tree, err := goMutantsTree(j)
	if err != nil {
		logf(os.Stdout, "aphrollo: no isolated tree to mutate in: %v", err)
		appendGateLog("mutants", logToken(j.Repo), "mutants-go", "mutants-refused:no-isolated-tree", 0)
		return 0
	}
	j.Worktree = tree
	out := filepath.Join(j.TargetDir, mutantsRunDir, "gremlins.json")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		logf(os.Stdout, "aphrollo: %v", err)
		return 0
	}
	jobs, why := mutantsJobsForThisBox()
	logf(os.Stdout, "aphrollo: %d worker(s) — %s", jobs, why)

	start := time.Now()
	code := goMutantsJobRunFn(j, out, jobs)
	data, err := os.ReadFile(out)
	if err != nil {
		logf(os.Stdout, "aphrollo: gremlins wrote no report (exit %d): %v", code, err)
		recordMutantsDeath(j, code, mutantsDeathTail(j))
		return 0
	}
	mutants, err := parseGremlinsReport(data)
	if err != nil {
		logf(os.Stdout, "aphrollo: unreadable gremlins report: %v", err)
		recordMutantsDeath(j, code, mutantsDeathTail(j))
		return 0
	}
	writeGoMutantsReceipt(j, mutants, treeStateAt(j.RepoRoot, j.Tip))
	MergeMutantStore(j.Repo, mutants)
	clearMutantsDeath(j.TipTree)
	appendGateLog("mutants", logToken(j.Repo), "mutants-go", "mutants-finished:"+short(j.TipTree), time.Since(start))
	return 0
}

// goMutantsJobRunFn is the local job's spawn, as a seam: a test proves where
// the run happens without a mutation tool on the box.
var goMutantsJobRunFn = runGremlins

// runGremlins runs the tool in the job's run tree, with the run's own temp
// dirs and target dir. Its output is this process's, which the parent pointed
// at the job's log files.
func runGremlins(j MutantsJob, outPath string, workers int) int {
	cmd := exec.Command(gremlinsBin, gremlinsArgv(j.BaseSHA, outPath, workers, nil)...)
	cmd.Dir = j.Worktree
	cmd.Env = mutantsChildEnv(j, nil)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		logf(os.Stdout, "aphrollo: %v", err)
		return 1
	}
	return 0
}

// runCommandIn runs one command in dir with this process's own output, and
// reports its exit code. It is the CI half's spawn: no detached job's env, no
// log files — the workflow's own log is where the tool's output belongs.
func runCommandIn(dir, bin string, args []string) int {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		logf(os.Stdout, "aphrollo: %v", err)
		return 1
	}
	return 0
}

// goMutantsRun is what a receipt needs to name the run it describes, whether
// that run was the detached local job or the pull request's own.
type goMutantsRun struct {
	Repo, RepoID, Branch, TipTree, BaseRef, BaseSHA, Worktree string
}

// writeGoMutantsReceipt renders one detached run into the receipt every merge
// reads, and writes it to the machine's receipt store.
func writeGoMutantsReceipt(j MutantsJob, mutants []MutantOutcome, now TreeState) {
	r := goMutantsReceipt(goMutantsRun{
		Repo: j.Repo, RepoID: j.RepoID, Branch: j.Branch, TipTree: j.TipTree,
		BaseRef: j.BaseRef, BaseSHA: j.BaseSHA, Worktree: j.Worktree,
	}, mutants, now)
	if path := MutationReceiptPathFor(j.TipTree); path != "" {
		writeReceiptFile(path, r)
	}
}

// goMutantsReceipt renders one run into the receipt every merge reads, and
// signs it. The verdict is "pass" whatever the numbers say: the runner
// reports and the merge gate judges — a runner that decided its own verdict
// would be marking its own homework. It also fills in each mutant's package,
// blob and fence in place, which is what the store carries forward.
func goMutantsReceipt(j goMutantsRun, mutants []MutantOutcome, now TreeState) MutationReceipt {
	r := MutationReceipt{
		Repo: j.Repo, RepoID: j.RepoID, Branch: j.Branch, TipTree: j.TipTree,
		BaseRef: j.BaseRef, BaseSHA: j.BaseSHA,
		Verdict: receiptVerdictPass, FinishedAt: time.Now().UTC(),
		Files: map[string]string{}, Fences: map[string]string{},
	}
	var survivors, timedOut []MutantOutcome
	for i, m := range mutants {
		m.Package = now.Packages[m.File]
		m.Blob, m.Fence = now.Blobs[m.File], now.Fences[m.Package]
		mutants[i] = m
		r.MutantsTotal++
		switch m.Status {
		case "caught":
			r.Caught++
		case "timeout":
			// Counted below, once the accept-list has had its say: a mutant
			// whose argument is that NO run can measure it would otherwise
			// block every merge forever.
			timedOut = append(timedOut, m)
		case "unviable":
			r.Unviable++
		case gremlinsNotCovered:
			r.NotCovered++
		default:
			survivors = append(survivors, m)
		}
		if m.Blob != "" {
			r.Files[m.File] = m.Blob
		}
		if m.Fence != "" {
			r.Fences[m.Package] = m.Fence
		}
	}
	r.Outcomes = mutants
	accepted, unaccepted := splitAcceptedSurvivors(j.Worktree, survivors)
	// A timeout is normally an UNMEASURED mutant rather than a result, and the
	// merge gate refuses one for exactly that reason. But some mutants cannot
	// be measured by any run: an INCREMENT_DECREMENT on a loop index cancels
	// the loop's own increment, so the function never returns and there is no
	// value to assert and no message to match. The only observable is the
	// absence of progress. Acceptance is the sole available answer, so it is
	// read here too -- through the same list, which still requires a stated
	// reason.
	//
	// The list is keyed on file:line MUTATOR and nothing reads the argument
	// itself, so an entry written about a SURVIVOR at that coordinate also
	// waives a timeout there -- including one caused by a slow box rather
	// than by non-termination. Every accepted timeout is therefore named in
	// the receipt below, so the waiver is auditable instead of silent.
	acceptedTimeouts, unmeasured := splitAcceptedSurvivors(j.Worktree, timedOut)
	r.Timeout = len(unmeasured)
	r.Accepted = len(accepted) + len(acceptedTimeouts)
	// An accepted timeout is NAMED in Survivors, exactly as an accepted
	// survivor is. That list is how the decision survives: a carried receipt
	// is recounted from its outcomes by recountReceipt, which has no
	// accept-list of its own and honours the producer by reading the names --
	// a mutant listed as a survivor and NOT as unaccepted. Recorded nowhere,
	// the acceptance would be re-derived as an unmeasured mutant the first
	// time the lane carried outcomes forward instead of re-measuring.
	for _, m := range acceptedTimeouts {
		r.Survivors = append(r.Survivors, m.name())
	}
	for _, m := range survivors {
		r.Survivors = append(r.Survivors, m.name())
	}
	for _, m := range unaccepted {
		r.Unaccepted = append(r.Unaccepted, m.name())
	}
	r.WorktreeDirty = worktreeDirty(j.Worktree)
	signReceipt(&r)
	return r
}

// worktreeDirty reports whether TRACKED files in the worktree differ from the
// commit being measured. Untracked files are excluded deliberately: the run's
// own build dir, logs and report live in there, and counting them would make
// every run report itself dirty.
func worktreeDirty(worktree string) bool {
	out, err := git(worktree, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// splitAcceptedSurvivors divides survivors by the repo's own accept-list,
// mutation-accept under [aphrollo] in aphrollo.toml. An entry reads
// "<file>:<line> <MUTATOR> # why it is acceptable", and the reason is not
// decoration: an accept-list nobody had to justify is a list of survivors
// somebody silenced.
func splitAcceptedSurvivors(root string, survivors []MutantOutcome) (accepted, unaccepted []MutantOutcome) {
	list := acceptedMutants(root)
	for _, m := range survivors {
		if list[survivorKey(m.File, m.Line, m.Mutation)] {
			accepted = append(accepted, m)
			continue
		}
		unaccepted = append(unaccepted, m)
	}
	return accepted, unaccepted
}

func survivorKey(file string, line int, mutation string) string {
	return filepath.ToSlash(file) + ":" + strconv.Itoa(line) + " " + strings.TrimSpace(mutation)
}

// acceptedMutants reads the accept-list, keeping only entries that state a
// reason.
func acceptedMutants(root string) map[string]bool {
	out := map[string]bool{}
	for _, entry := range tomlStringsIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", "mutation-accept") {
		key, reason, ok := strings.Cut(entry, "#")
		if !ok || strings.TrimSpace(reason) == "" {
			continue
		}
		out[strings.TrimSpace(key)] = true
	}
	return out
}
