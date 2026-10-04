package postedit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitclient "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/kernel"
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

// runUnit names a run's unit in the store: the project the run was for.
func runUnit(root string) string { return filepath.ToSlash(root) }

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
	res := phaseSuiteResult(j, out)
	switch {
	case res.Inconclusive != "":
		return kernel.VerdictNotTested, causeSkipped, true
	case out.ExitCode == 0:
		if j.Phase != "run" {
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
	ctx, cancel := context.WithTimeout(context.Background(), runVerdictBudget)
	defer cancel()
	s, err := openRunStoreFn(j.Project)
	if err == nil {
		_, err = s.RecordVerdict(ctx, key, filepath.Base(j.Project), store.Verdict{Runs: []store.RunVerdict{{
			Runner: runner.Cmd, Unit: runUnit(j.Project), Result: result, Cause: cause, Test: failing,
			MS: int64(out.Seconds * 1000), At: time.Now().UTC(),
		}}})
	}
	if err != nil {
		AppendGateLog("postedit", j.Project, cmdString(runner), "store-write-failed", 0)
		return ""
	}
	if isRedResult(result) {
		writeLaneRed(laneRedPointer{Root: j.Project, Key: key})
	} else {
		removeLaneRed(j.Project)
	}
	return result
}

// laneRedPointer says the last run of Root ended red on tree Key. It exists so
// the Stop check, which runs at every turn's end, finds out there is nothing
// to read from the store with one directory listing and no git spawn.
type laneRedPointer struct {
	Root string `json:"root"`
	Key  string `json:"key"`
}

func laneRedDir() string {
	if base := StateDir(); base != "" {
		return filepath.Join(base, "lane-red")
	}
	return ""
}

func laneRedFile(root string) string {
	if dir := laneRedDir(); dir != "" {
		return filepath.Join(dir, projectKey(root)+".json")
	}
	return ""
}

func writeLaneRed(p laneRedPointer) {
	path := laneRedFile(p.Root)
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	if data, err := json.Marshal(p); err == nil {
		// A pointer that cannot be written only hides the red from the store
		// read; the session's job record still carries it.
		_ = writeFileAtomic(path, data)
	}
}

func removeLaneRed(root string) {
	if path := laneRedFile(root); path != "" {
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
		if json.Unmarshal(data, &p) == nil && p.Root != "" && deferredProjectWithin(p.Root, tree) {
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
	sweepSeen(filepath.Dir(path))
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

// sweepSeen removes the marks older than seenKeep.
func sweepSeen(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > seenKeep {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// markOutcomeSeen marks the red a phase's own line is about as told to session.
func markOutcomeSeen(session string, out PhaseOutcome) {
	if out.TreeKey != "" && isRedResult(out.StoreResult) {
		markRedSeen(session, out.TreeKey)
	}
}

// storeRedReason is the block reason for a red the store holds on the tree
// stands as it is now that the session has not been told of, "" when there is
// none. Telling it marks it seen, so the next check allows. A red of an older
// tree is on another key and never blocks; a green beside a red of one unit on
// one tree is a flake, not a red to stop on; a run that was not tested allows.
func storeRedReason(session, tree string) string {
	if len(laneRedsWithin(tree)) == 0 {
		return ""
	}
	key, err := worktreeKeyFn(tree)
	if err != nil || key == "" || redSeen(session, key) {
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
	for _, r := range v.Runs {
		if !isRedResult(r.Result) || !deferredProjectWithin(r.Unit, tree) || hasGreen(v, r.Runner, r.Unit) {
			continue
		}
		markRedSeen(session, key)
		line := "gate: " + r.Runner + " in " + r.Unit + " → red on this tree"
		if r.Test != "" {
			line += "; failing: " + r.Test
		}
		return unseenRedPreface + "\n" + strings.TrimSpace(line)
	}
	return ""
}

// hasGreen reports a green run of the runner and unit on the tree.
func hasGreen(v store.Verdict, runner, unit string) bool {
	for _, r := range v.RunsOf(runner, unit) {
		if r.Result == kernel.VerdictGreen {
			return true
		}
	}
	return false
}
