package postedit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitclient "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// A run that reaches a verdict is written to the lane's store (internal/store,
// verdicts/<worktree key>.json) by the run's own wrapper, in the process that
// ran it, the moment its tests end. That is the one place the tree the tests
// judged is known (the key is read before they start) and the one place a git
// spawn costs a hook nothing. The Stop checks read the store, so a verdict
// outlives the hook that prints it and is judged against the tree it measured,
// not against whichever job record happens to be on disk.
//
// The write is best-effort: it never fails the run, waits no longer than
// runVerdictBudget, and a failure is logged. The Stop checks then fall back to
// the session's job records, which is what they read before the store.

// runVerdictBudget bounds the store's lock wait and write for one verdict.
const runVerdictBudget = 150 * time.Millisecond

// worktreeKeyFn names the tree root stands in as it is now (git.WorktreeKey).
// A seam, so a test states the tree without one.
var worktreeKeyFn = func(root string) (string, error) {
	c, err := gitclient.New(root, gitclient.Options{})
	if err != nil {
		return "", err
	}
	return c.WorktreeKey()
}

// openRunStoreFn opens the store of the repo root belongs to.
var openRunStoreFn = func(root string) (*store.Store, error) {
	return store.Open(EventLogDir(root), store.Options{Warn: func(string) {}})
}

// runUnitOf names a run's unit in the store: the project the run was for,
// relative to the repository root, and the command it ran without the -timeout
// the deferral adds, which is the package set. Two runs of one project over
// different packages are two units: a green of one never hides a red of the
// other.
func runUnitOf(j DeferredJob) string {
	rel := filepath.ToSlash(j.Project)
	if top := RepoRoot(j.Project); top != "" {
		if r, err := filepath.Rel(top, j.Project); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	return rel + "|" + strings.Join(queuedArgv(j), " ")
}

func isRedResult(v kernel.Verdict) bool {
	return v == kernel.VerdictRed || v == kernel.VerdictRedMissingImpl || v == kernel.VerdictRedBogus
}

// The causes a not-tested run is recorded with (kernel.Cause*).
const (
	causeInfra   = kernel.CauseInfra
	causeSkipped = kernel.CauseSkipped
	causeTimeout = kernel.CauseTimeout
)

// phaseVerdict is what a finished phase established about the code, the same
// reading failedTheCode gives the Stop check: a failing exit is a red unless the
// phase never ran, was cut short, ran no test or only timed out; a passing run
// phase is a green. ok is false for a phase that is no verdict (a build that
// compiled has not run a test).
func phaseVerdict(j DeferredJob, out PhaseOutcome) (result kernel.Verdict, cause string, ok bool) {
	if out.SetupFailed {
		return kernel.VerdictNotTested, causeInfra, true
	}
	return phaseVerdictOf(j.Phase, phaseSuiteResult(j, out), out)
}

// phaseVerdictOf is phaseVerdict over a result the caller has read already, so a
// caller that holds one does not read the phase's log a second time.
func phaseVerdictOf(phase string, res SuiteResult, out PhaseOutcome) (result kernel.Verdict, cause string, ok bool) {
	if out.SetupFailed {
		return kernel.VerdictNotTested, causeInfra, true
	}
	switch {
	case res.Inconclusive != "":
		return kernel.VerdictNotTested, causeSkipped, true
	case out.ExitCode == 0:
		if phase != "run" {
			return "", "", false
		}
		if runnerTimeoutsOnly(res.Output) {
			return kernel.VerdictNotTested, causeTimeout, true
		}
		return kernel.VerdictGreen, "", true
	case runnerTimeoutsOnly(res.Output):
		return kernel.VerdictNotTested, causeTimeout, true
	case treatAsEmptyPass(res):
		return kernel.VerdictNotTested, causeSkipped, true
	}
	return kernel.VerdictRed, "", true
}

// recordPhaseVerdict writes a finished phase's verdict to the store under key,
// the tree the phase ran on, and answers the result it recorded ("" when it
// recorded none). A red also leaves the lane-red pointer the Stop checks look
// for first; any other verdict removes it, for the run is newer than the red.
func recordPhaseVerdict(j DeferredJob, out PhaseOutcome, key string) kernel.Verdict {
	result, cause, ok := phaseVerdict(j, out)
	if !ok || key == "" {
		return ""
	}
	failing := ""
	if isRedResult(result) {
		if tests := ExtractFailingTests(deferredLog(j)); len(tests) > 0 {
			failing = tests[0]
		}
	}
	runner := runnerFromArgv(j.Runner, j.Dir)
	unit := runUnitOf(j)
	// The pointer follows the verdict, not the store write: a green whose write
	// failed still clears its unit's red, and a red whose write failed leaves
	// none, for the store holds nothing for Stop to read.
	if isRedResult(result) {
		writeLaneRed(laneRedPointer{Root: j.Project, Key: key, Unit: unit})
	} else {
		removeLaneRed(j.Project, unit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runVerdictBudget)
	defer cancel()
	s, err := openRunStoreFn(j.Project)
	if err == nil {
		_, err = s.RecordVerdict(ctx, key, filepath.Base(j.Project), store.Verdict{Runs: []store.RunVerdict{{
			Runner: runner.Cmd, Unit: unit, Result: result, Cause: cause, Test: failing,
			MS: int64(out.Seconds * 1000), At: time.Now().UTC(),
		}}})
	}
	if err != nil {
		AppendGateLog("postedit", j.Project, cmdString(runner), "store-write-failed", 0)
		removeLaneRed(j.Project, unit)
		return ""
	}
	return result
}

// laneRedPointer says the last run of Root ended red on tree Key. It exists so
// the Stop check, which runs at every turn's end, finds out there is nothing
// to read from the store with one directory listing and no git spawn.
type laneRedPointer struct {
	Root string `json:"root"`
	Key  string `json:"key"`
	Unit string `json:"unit"`
}

func laneRedDir() string {
	if base := StateDir(); base != "" {
		return filepath.Join(base, "lane-red")
	}
	return ""
}

func laneRedFile(root, unit string) string {
	if dir := laneRedDir(); dir != "" {
		sum := sha256.Sum256([]byte(root + "|" + unit))
		return filepath.Join(dir, hex.EncodeToString(sum[:8])+".json")
	}
	return ""
}

func writeLaneRed(p laneRedPointer) {
	path := laneRedFile(p.Root, p.Unit)
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	if data, err := json.Marshal(p); err == nil {
		// A pointer that cannot be written only hides the red from the store
		// read; the session's job record still carries it.
		_ = writeFileAtomic(path, data)
	}
}

func removeLaneRed(root, unit string) {
	if path := laneRedFile(root, unit); path != "" {
		_ = os.Remove(path)
	}
}

// anyLaneRed reports whether any pointer exists: the one listing the Stop check
// pays when there is nothing to read.
func anyLaneRed() bool {
	entries, err := os.ReadDir(laneRedDir())
	return err == nil && len(entries) > 0
}

// laneRedsWithin lists the pointers of projects inside tree.
func laneRedsWithin(tree string) []laneRedPointer {
	var out []laneRedPointer
	for _, p := range readLaneReds() {
		if deferredProjectWithin(p.Root, tree) {
			out = append(out, p)
		}
	}
	return out
}

// readLaneReds lists every pointer, skipping one that does not read.
func readLaneReds() []laneRedPointer {
	dir := laneRedDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []laneRedPointer
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p laneRedPointer
		if json.Unmarshal(data, &p) == nil && p.Root != "" {
			out = append(out, p)
		}
	}
	return out
}

// seenDir holds the marks of the reds a session has been told, one file per
// (session, tree key).
func seenDir() string {
	if base := StateDir(); base != "" {
		return filepath.Join(base, "stop-seen")
	}
	return ""
}

// seenKeep is how long a told-mark is kept: past it the red it names is of a
// tree long gone.
const seenKeep = 24 * time.Hour

func seenMark(session, key string) string {
	dir := seenDir()
	if dir == "" || session == "" || key == "" {
		return ""
	}
	return filepath.Join(dir, sessionKey(session)+"-"+key[:min(len(key), 32)])
}

// markRedSeen records that session has been told of the red on tree key. The
// hook that prints a run's red line calls it, and so does the block that
// carries one.
func markRedSeen(session, key string) {
	path := seenMark(session, key)
	// A mark that cannot be written costs one more block, never a lost one.
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, nil, 0o600)
}

func redSeen(session, key string) bool {
	path := seenMark(session, key)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// sweepLaneState is the part of the state sweep that is the Stop check's: the
// told-marks older than seenKeep, and the pointers of a project whose worktree
// is gone from the disk, which no Stop in it will ever reach to clear.
func sweepLaneState(now time.Time) int {
	removed := 0
	for _, dir := range []string{seenDir(), lintNoticeDir(), lintLatestDir()} {
		if dir == "" {
			continue
		}
		entries, _ := os.ReadDir(dir) // absent is nothing to sweep
		for _, e := range entries {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > seenKeep && os.Remove(filepath.Join(dir, e.Name())) == nil {
				removed++
			}
		}
	}
	for _, p := range readLaneReds() {
		if _, err := os.Stat(p.Root); os.IsNotExist(err) {
			removeLaneRed(p.Root, p.Unit)
			removed++
		}
	}
	return removed
}

// markOutcomeSeen marks the red a phase's own line is about as told to session.
func markOutcomeSeen(session string, out PhaseOutcome) {
	if out.TreeKey != "" && isRedResult(out.StoreResult) {
		markRedSeen(session, out.TreeKey)
	}
}

// storeRedReason is the block reason for the reds the store holds on the tree
// as it stands now that the session has not been told of, "" when there are
// none. Telling them marks them seen, so the next check allows. A pointer to a
// red of another tree is stale: it is removed, so the key is read at most once
// for a tree that moved. A unit is red when it has a red run on the tree and no
// green of its own, so a green of one package hides no red of another; a green
// beside a red of one unit is a flake, not a red to stop on, and a run that was
// not tested allows.
func storeRedReason(session, tree string) string {
	pointers := laneRedsWithin(tree)
	if len(pointers) == 0 {
		return ""
	}
	key, err := worktreeKeyFn(tree)
	if err != nil || key == "" {
		return ""
	}
	var current []laneRedPointer
	for _, p := range pointers {
		if p.Key == key {
			current = append(current, p)
		} else {
			removeLaneRed(p.Root, p.Unit)
		}
	}
	if len(current) == 0 || redSeen(session, key) {
		return ""
	}
	s, err := openRunStoreFn(tree)
	if err != nil {
		return ""
	}
	v, found, err := s.ReadVerdict(key)
	if err != nil || !found {
		return ""
	}
	var lines []string
	for _, p := range current {
		if r, ok := redRunOf(v, p.Unit); ok {
			line := "gate: " + p.Unit + " → red on this tree"
			if r.Test != "" {
				line += "; failing: " + r.Test
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	markRedSeen(session, key)
	return unseenRedPreface + "\n" + strings.Join(lines, "\n")
}

// redRunOf is a red run of unit on the tree, when it has one and no green.
func redRunOf(v store.Verdict, unit string) (store.RunVerdict, bool) {
	var red store.RunVerdict
	found := false
	for _, r := range v.Runs {
		switch {
		case r.Unit != unit:
		case r.Result == kernel.VerdictGreen:
			return store.RunVerdict{}, false
		case isRedResult(r.Result) && !found:
			red, found = r, true
		}
	}
	return red, found
}

// queueShadowRun holds, for the shadow record, what the kernel makes of a finished
// run beside aphrollo's line for it (word is the verdict word it logged, "" when
// its line is no verdict). Every run is held, the foreground one and the harvested
// alike; one that is no verdict on either side is recorded as unjudged. It reads
// nothing: res is the result already read, taken before any adjustment, and the
// record is written by shadow.Flush after the hook has answered. The run is also
// held to be folded into its lane's record (shadow.QueueFold): editID names the
// edits it judged, and the tree key the run wrapper read is the tree they made.
func queueShadowRun(phase string, out PhaseOutcome, res SuiteResult, root, session, editID string, argv []string, word string) {
	verdict, cause, ok := phaseVerdictOf(phase, res, out)
	src := shadow.Source{Root: root, Actor: session, Key: out.TreeKey}
	if len(argv) > 0 {
		src.Lang = shadow.LangOfCommand(argv[0])
	}
	shadow.QueueRun(src, func() (shadow.RunFact, bool) {
		return shadow.RunFact{Word: word, Verdict: verdict, Cause: cause}, ok && word != ""
	})
	if ok {
		shadow.QueueFold(src, shadowWorld(), shadow.Fold{
			Root: root, Actor: session, Tree: out.TreeKey, Job: out.RunID,
			EditIDs: strings.Split(editID, shadowEditIDSep), Argv: argv, Verdict: verdict, Cause: cause,
		})
	}
}

// shadowEditIDSep is the separator failfirst joins the ids of the edits one run judged by.
const shadowEditIDSep = ","

// queueForegroundRun is queueShadowRun for a run the hook itself ran to its end,
// which carries no phase outcome: the exit it would have is read from the result,
// and the tree is the key of the run it widened, when it did.
func queueForegroundRun(res SuiteResult, root, session, editID, treeKey string, argv []string, word string) {
	out := PhaseOutcome{TreeKey: treeKey}
	if !res.Passed {
		out.ExitCode = 1
	}
	queueShadowRun("run", out, res, root, session, editID, argv, word)
}
