package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

// The stop checks answer the three hooks that end something: Stop (the turn),
// SubagentStop (a subagent's turn) and TaskCompleted (a task marked done). One
// function, DecideStop, holds every decision, so the one place that asks
// whether the checks run at all is the one place a setting can later switch
// them.
//
// What they guard is a red nobody was told. A run the edit hook left detached
// finishes between hooks; the next hook reports it and clears its record, so a
// record that is still on disk with a failed result is a red Claude has never
// seen. Stop and SubagentStop hand it over once, as the reason to go on;
// TaskCompleted also holds the task open for any project of its tree whose
// last run failed, seen or not.

// StopEvent names the hook that asked.
type StopEvent string

const (
	StopHookStop          StopEvent = "Stop"
	StopHookSubagentStop  StopEvent = "SubagentStop"
	StopHookTaskCompleted StopEvent = "TaskCompleted"
)

// StopVerdict is what a stop check decided: Block keeps the turn or the task
// going, and Reason is the text Claude is shown.
type StopVerdict struct {
	Block  bool
	Reason string
	// Red is aphrollo's own finding at a Stop or SubagentStop: the actor has a red it
	// had not been told of outstanding (the lane's own or a finished deferred
	// run's), whatever the check then did about it (it allows under stop_hook_active
	// and with the gate off). RedTrees are the trees of the lane's reds it is about,
	// none for a red only a job record holds. The shadow record of the kernel's
	// stop-red rule reads both; they change nothing in what is rendered.
	Red      bool
	RedTrees []string
}

// stopInput is the part of the Stop, SubagentStop and TaskCompleted payloads
// the checks read. stop_hook_active is true while Claude is already continuing
// because an earlier Stop hook blocked.
type stopInput struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// stopTreeFn resolves the checkout a hook's cwd stands in: the lane a subagent
// works in, the tree a task ran in. A seam, so the path with nothing to report
// can be shown to resolve none.
var stopTreeFn = func(cwd string) string {
	if root := RepoRoot(cwd); root != "" {
		return root
	}
	return cwd
}

// DecideStop is the one decision behind the three hooks. Every path that
// cannot read what it needs allows: a stop check that fails must never hold a
// session in a turn.
func DecideStop(event StopEvent, raw []byte) StopVerdict {
	var in stopInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return StopVerdict{}
	}
	// The unseen-red fact is read before the check, which marks what it tells seen.
	var red bool
	var trees []string
	if event != StopHookTaskCompleted {
		red, trees = unseenRedFact(event, in.SessionID, in.Cwd)
	}
	v := decideStop(event, in)
	if event != StopHookTaskCompleted {
		v.Red, v.RedTrees = red, trees
	}
	return v
}

// unseenRedFact says whether the actor has a red it has not been told of: a lane
// red the store holds for the tree cwd stands in (its tree key is returned), or a
// finished deferred run of the session (the whole session for a Stop, the cwd's
// checkout for a SubagentStop). It marks and removes nothing.
func unseenRedFact(event StopEvent, session, cwd string) (bool, []string) {
	if session == "" {
		return false, nil
	}
	var trees []string
	if cwd != "" && anyLaneRed() {
		tree := stopTreeFn(cwd)
		// The pointers' own keys, unchecked against the tree as it is now: that is a
		// git spawn the check itself makes once and this read must not add.
		for _, p := range laneRedsWithin(tree) {
			if !redSeen(session, p.Key) && !slices.Contains(trees, p.Key) {
				trees = append(trees, p.Key)
			}
		}
	}
	if len(trees) > 0 {
		return true, trees
	}
	reds := finishedReds(session)
	if len(reds) > 0 && event == StopHookSubagentStop && cwd != "" {
		reds = redsWithin(reds, stopTreeFn(cwd))
	}
	return len(reds) > 0, nil
}

// decideStop is DecideStop's check, over a payload already read.
func decideStop(event StopEvent, in stopInput) StopVerdict {
	state, _ := loadSession(in.SessionID)
	if !stopCheckEnforced(state) {
		return StopVerdict{}
	}
	switch event {
	case StopHookStop:
		if in.StopHookActive {
			return StopVerdict{}
		}
		if v := laneRedVerdict(in.SessionID, in.Cwd); v.Block {
			return v
		}
		return unseenRedVerdict(in.SessionID, "")
	case StopHookSubagentStop:
		if in.StopHookActive || in.Cwd == "" {
			return StopVerdict{}
		}
		if v := laneRedVerdict(in.SessionID, in.Cwd); v.Block {
			return v
		}
		return unseenRedVerdict(in.SessionID, in.Cwd)
	case StopHookTaskCompleted:
		if in.Cwd == "" {
			return StopVerdict{}
		}
		return taskVerdict(in.SessionID, in.Cwd, state)
	}
	return StopVerdict{}
}

// stopCheckEnforced says whether the stop checks run for this session. Today
// only the session's own switch answers (/tdd off), and a payload with no
// session id has no state to judge; the setting that grades the gate as
// enforce, warn or off reads here.
func stopCheckEnforced(state *sessionState) bool {
	return state != nil && !state.Overrides.Off
}

const (
	unseenRedPreface = "gate: a deferred run finished red after your last hook, so you have not seen it yet. Read it before you stop:"
	taskOpenPreface  = "gate: task kept open — its tests are red. Make them green before you mark it done:"
)

// laneRedVerdict is the lane's own answer: a red the store holds for the tree
// cwd stands in as it is now, that session has not been told of. It reads
// nothing, and resolves no tree, while no run of the box ended red, and a
// payload with no session has nobody to tell. The block is the telling for the
// session's own finished-red job records of that tree too, so the fallback does
// not block the same run a second time.
func laneRedVerdict(session, cwd string) StopVerdict {
	if session == "" || cwd == "" || !anyLaneRed() {
		return StopVerdict{}
	}
	tree := stopTreeFn(cwd)
	reason := storeRedReason(session, tree)
	if reason == "" {
		return StopVerdict{}
	}
	if lines := deliverReds(session, redsWithin(finishedReds(session), tree)); len(lines) > 0 {
		reason += "\n" + strings.Join(lines, "\n")
	}
	return StopVerdict{Block: true, Reason: reason, Red: true}
}

// unseenRedVerdict blocks once with the verdict line of every unseen red:
// those of the whole session when lane is empty, otherwise only those in the
// checkout lane stands in.
func unseenRedVerdict(session, lane string) StopVerdict {
	reds := finishedReds(session)
	if len(reds) > 0 && lane != "" {
		reds = redsWithin(reds, stopTreeFn(lane))
	}
	if len(reds) == 0 {
		return StopVerdict{}
	}
	return StopVerdict{Block: true, Reason: unseenRedPreface + "\n" + strings.Join(deliverReds(session, reds), "\n"), Red: true}
}

// taskVerdict keeps a task open while the tree it ran in is red: an unseen red
// is delivered first, then every project of the tree whose last recorded run
// failed under the git state it is in now is named with its failing tests.
// There is no second chance to allow it, because the task stays open for as
// long as the tests are red.
func taskVerdict(session, cwd string, state *sessionState) StopVerdict {
	reds := finishedReds(session)
	if len(reds) == 0 && !hasFailedProject(state) {
		return StopVerdict{}
	}
	tree := stopTreeFn(cwd)
	lines := deliverReds(session, redsWithin(reds, tree))
	lines = append(lines, failedProjectLines(state, tree)...)
	if len(lines) == 0 {
		return StopVerdict{}
	}
	return StopVerdict{Block: true, Reason: taskOpenPreface + "\n" + strings.Join(lines, "\n")}
}

// finishedReds lists the session's jobs whose run finished red and that no
// hook has reported: a result that is a failure of the code, not a phase that
// never ran, was cut short, ran no test, or timed out on every test. Reading
// the record and its result is all it costs; nothing is judged or cleared. A
// job still running has no result, and the empty outcome is a clean exit.
func finishedReds(session string) []DeferredJob {
	var reds []DeferredJob
	for _, j := range sessionDeferredJobs(session) {
		out, _ := deferredResult(j)
		if failedTheCode(j, out) {
			reds = append(reds, j)
		}
	}
	return reds
}

// failedTheCode reports whether a finished phase's result is a red. A build
// that compiled has not run a test yet, so only a failed exit counts.
func failedTheCode(j DeferredJob, out PhaseOutcome) bool {
	if out.ExitCode == 0 || out.SetupFailed {
		return false
	}
	res := phaseSuiteResult(j, out)
	return res.Inconclusive == "" && !runnerTimeoutsOnly(res.Output) && !treatAsEmptyPass(res)
}

// redsWithin keeps the jobs whose project lies in tree.
func redsWithin(jobs []DeferredJob, tree string) []DeferredJob {
	var kept []DeferredJob
	for _, j := range jobs {
		if deferredProjectWithin(j.Project, tree) {
			kept = append(kept, j)
		}
	}
	return kept
}

// deliverReds reports each job through the harvest every hook shares, which
// judges it, stamps the session and clears the record: what it returns is the
// gate line Claude would have read at its next hook, so no later hook tells it
// again.
func deliverReds(session string, jobs []DeferredJob) []string {
	lines := make([]string, 0, len(jobs))
	for _, j := range jobs {
		lines = append(lines, harvestSessionJob(session, j))
	}
	return lines
}

// lastRunFailed reports whether an outcome is a run that ended with failing
// tests: any red, or no-delta, the same failures as the run before.
func lastRunFailed(outcome string) bool {
	return Outcome(outcome).IsRed() || Outcome(outcome) == NoDelta
}

func hasFailedProject(state *sessionState) bool {
	for _, ps := range state.ByProject {
		if lastRunFailed(ps.Outcome) {
			return true
		}
	}
	return false
}

// failedProjectLines names, project by project in a stable order, every
// project of tree whose last recorded run failed and whose git state has not
// moved since: a commit is gated on a green suite, so a red stamped before one
// is history and must not hold a task open.
func failedProjectLines(state *sessionState, tree string) []string {
	roots := make([]string, 0, len(state.ByProject))
	for root := range state.ByProject {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	var lines []string
	for _, root := range roots {
		ps := state.ByProject[root]
		if !lastRunFailed(ps.Outcome) || !deferredProjectWithin(root, tree) {
			continue
		}
		now := computeFingerprint(root)
		if ps.Fingerprint == nil || now == nil || *ps.Fingerprint != *now {
			continue
		}
		line := fmt.Sprintf("%s → outcome=%s", root, ps.Outcome)
		if len(ps.FailingTests) > 0 {
			line += "; failing tests:\n  " + strings.Join(ps.FailingTests, "\n  ")
		}
		lines = append(lines, line)
	}
	return lines
}

// RenderStopVerdict turns a verdict into what the harness reads. Stop and
// SubagentStop block with a decision on stdout and exit 0; they have no
// hookSpecificOutput, and a payload carrying one is dropped. TaskCompleted
// holds the task open with exit 2 and the reason on stderr. An allow renders
// nothing.
func RenderStopVerdict(event StopEvent, v StopVerdict) (stdout, stderr []byte, code int) {
	if !v.Block {
		return nil, nil, 0
	}
	if event == StopHookTaskCompleted {
		return nil, []byte(v.Reason + "\n"), 2
	}
	b, _ := json.Marshal(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{Decision: "block", Reason: v.Reason})
	return b, nil, 0
}

// RecordFinishedRedDeferredJobForTest records a finished, failed run phase for
// project under session, matching target's content as it stands, with failing
// named in its log: the unseen red a test in another package needs without
// re-deriving the record's on-disk layout.
func RecordFinishedRedDeferredJobForTest(project, target, session, failing string) {
	saveDeferredJob(DeferredJob{
		Project: project, Session: session, Phase: "run", Dir: project, PID: 4242,
		Started: time.Now(), File: target,
		HeadSHA: headSHAFor(project), FileHash: sourceIdentity(project, target),
		Runner: []string{"cargo", "test", "-p", "crate_a"},
	})
	job, _ := loadDeferredJob(session, project)
	log := "running 1 test\ntest " + failing + " ... FAILED\n\nfailures:\n    " + failing +
		"\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored\n"
	_ = os.WriteFile(job.Log, []byte(log), 0o600)
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 101, Seconds: 30})
}
