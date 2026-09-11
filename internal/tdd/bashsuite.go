package tdd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DecidePreEdit gates the four edit tools; PreBash/PostBash catch a shell
// EDIT. Neither judges the shell command itself, so a session could run
// `go test ./...` / `cargo test` / `cargo nextest run` unlimited times,
// uncounted, beside every suite the gate already scheduled. This file closes
// that hole: it judges the COMMAND, not the tree it might change, so it runs
// before PreBash's snapshot and never touches it.
//
// A false deny here is far worse than a false allow: refusing a legitimate
// rerun after an inconclusive TIMEOUT/SKIPPED verdict leaves a session with
// no way to get an answer at all. So every classifier below is written to
// answer "not sure" as "not narrowed" only where narrowing is genuinely
// absent, and "no fresh verdict" whenever the log does not clearly say
// otherwise — never the reverse.

// bashSuiteInput is the slice of a Bash/PowerShell PreToolUse payload this
// judgment needs; bashLikeTools (primary.go) is the shared tool-name set.
type bashSuiteInput struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// bashSuiteVerdictFreshFor bounds how long a logged green/red/blocked
// verdict speaks for a tree before a whole-suite rerun stops being redundant
// against it. Reusing redGoesStaleAfter rather than a second constant: both
// answer the same question — does an outcome logged a while ago still
// describe the tree a session is standing in right now.
const bashSuiteVerdictFreshFor = redGoesStaleAfter

// suiteShape is what one Bash/PowerShell command asks the gate to judge.
type suiteShape int

const (
	// notSuiteInvocation: no segment of the command runs go test / cargo
	// test / cargo nextest run at all — nothing here is this hook's concern.
	notSuiteInvocation suiteShape = iota
	// narrowedSuiteInvocation: a -run/-p/filter/single-package narrowing is
	// present. This is also the shape a hand-run mutation proof takes (one
	// targeted test, mutate, rerun) — the two are not distinguished, because
	// a mutation proof IS a narrowed invocation by construction.
	narrowedSuiteInvocation
	// wholeSuiteInvocation: the runner would exercise the whole project (or
	// whole workspace), no filter, no package, no -run.
	wholeSuiteInvocation
)

// DecideBashSuite judges a Bash/PowerShell PreToolUse payload against a
// redundant whole-suite invocation. The second return is false whenever the
// payload is not a bashLikeTools call, or names no test-runner invocation at
// all — callers must not log or act on a Decision that was never judged.
func DecideBashSuite(raw []byte) (Decision, bool) {
	var in bashSuiteInput
	if err := json.Unmarshal(raw, &in); err != nil || !bashLikeTools[in.ToolName] {
		return Decision{}, false
	}
	cmd := strings.TrimSpace(in.ToolInput.Command)
	if cmd == "" {
		return Decision{}, false
	}
	shape := notSuiteInvocation
	for _, words := range shellSegments(stripHeredocBodies(cmd)) {
		if s := classifySuiteSegment(words); s > shape {
			shape = s
		}
	}
	if shape == notSuiteInvocation {
		return Decision{}, false
	}
	// A soak is deliberate and rare by nature — like queue-bypass, what makes
	// it tolerable is that every use is counted, regardless of what else the
	// gate.log already holds for this tree.
	if hasSoakMarker(cmd) {
		return Decision{Action: Allow, Escapes: []string{"override-bash-soak"}}, true
	}
	// The root is the one the COMMAND enters, not the one the session's cwd
	// names: those differ whenever a run is issued into a lane worktree with
	// `cd <lane> && …`, and judging a lane's run against the primary's
	// verdict refused runs that had never happened (issue #645).
	root := effectiveRunRoot(in.Cwd, cmd)
	if shape == narrowedSuiteInvocation {
		return decideNarrowedSuite(root, cmd), true
	}
	return decideWholeSuite(root), true
}

// decideNarrowedSuite judges a narrowed rerun against what the gate already
// answered for this tree. Three outcomes, and the split is the whole of issue
// #572:
//
//   - A fresh SETTLED verdict (green/red) on record: blocked. The narrowing
//     rule was written so that a rerun after an INCONCLUSIVE verdict is never
//     refused, which is right; an unconditional allow also let a narrowed run
//     stand beside a green or red the hook had just delivered — the redundant
//     run the policy forbids, and 78 override-bash-narrowed lines in seven
//     days. The refusal names the verdict and its age so the session reads
//     that line instead of re-running for it.
//   - Recent activity that is NOT a settled verdict (TIMEOUT, SKIPPED,
//     QUEUED-SKIPPED, deferred-abandoned, infra-failed, a job still
//     deferred): allowed and counted. The code was not tested; this rerun is
//     the only route to an answer and must never be refused.
//   - Nothing recent at all: allowed, uncounted. That is the ordinary
//     single-test run a session makes while writing code, and counting it
//     would turn gate.log into a keystroke transcript.
//
// A mutation proof is the one narrowed rerun that legitimately stands beside
// a fresh green, and it says so for itself — see hasMutationProofMarker.
//
// The root is the tree the command actually runs in (effectiveRunRoot), so
// the freshness question is only ever asked of verdicts about THAT tree: a
// green logged for the checkout next door is not an answer this run would be
// redundant against.
func decideNarrowedSuite(root, cmd string) Decision {
	if root == "" {
		return Decision{Action: Allow}
	}
	if entry, fresh := lastFreshSuiteVerdict(root, attemptedScope(cmd)); fresh {
		if hasMutationProofMarker(cmd) {
			return Decision{Action: Allow, Escapes: []string{"override-bash-mutation-proof"}}
		}
		return Decision{
			Action: Block,
			Policy: "bash-narrowed-rerun",
			Reason: denyNarrowedRerunReason(root, entry),
		}
	}
	if _, recent := lastSuiteLogEntry(root, bashSuiteVerdictFreshFor); recent {
		return Decision{Action: Allow, Escapes: []string{"override-bash-narrowed"}}
	}
	return Decision{Action: Allow}
}

// denyNarrowedRerunReason names what the gate already holds: the verdict, the
// stage that produced it and how old it is — without the age a caller cannot
// tell an answer about the code just written from one about the tree an hour
// back — and BOTH routes to it, saying which answers which.
//
// Naming only `gate stats` made this refusal false for the commonest reason a
// session re-runs a suite it just watched the gate run: it wants the OUTPUT,
// not the verdict — one assertion line, at one file and line. `gate stats`
// cannot answer that, so the refusal was a dead end, and a dead end is
// answered by rewording the command (a real session met it with a six-shot
// retry loop against a 28-minute timeout). `gate output` serves the bytes of
// that very run, so the route out is the gate's own record rather than a
// second suite.
//
// The mutation marker comes LAST and says what it is. While the guard still
// resolved the checkout from the session cwd, MUTATION=1 was the only route
// past a refusal a session could not otherwise answer, and three ordinary
// suite runs went into gate.log labelled as mutation proofs (issue #645) — a
// marker that doubles as the escape hatch stops meaning what it says, and
// every count taken from it is then wrong. The refusal names the honest
// routes first and declines to present the marker as a general way through.
func denyNarrowedRerunReason(root string, e gateEntry) string {
	ago := time.Since(e.at).Round(time.Second)
	return fmt.Sprintf(
		"the gate already holds a %s verdict for %s — the tree this command runs in — from the %s "+
			"stage, logged %s ago: re-running one of its tests by hand answers nothing that run does "+
			"not already hold. Two routes to it: `aphrollo gate output` for the text that run actually "+
			"printed (its assertion lines, unfiltered), `aphrollo gate stats` for the verdict itself. "+
			"A narrowed rerun is for an INCONCLUSIVE verdict (TIMEOUT, SKIPPED, QUEUED-SKIPPED, %s, "+
			"%s, or none logged), where the code was never tested — that is the rerun this guard lets "+
			"through, and a run in a DIFFERENT checkout is never refused here at all. MUTATION=1 is "+
			"not a way past this refusal: it labels a run that IS a mutation proof, and every run "+
			"carrying it is counted as one.",
		e.verdict, root, e.stage, ago, DeferredAbandoned, InfraFailed)
}

// hasMutationProofMarker reports whether the raw command names a mutation
// proof deliberately. Same shape as hasSoakMarker, for the same reason: a
// mutation proof IS a narrowed rerun beside a fresh green by construction
// (break the code, run the one test, expect it to fail), so it cannot be told
// from a redundant one by looking at the command — the session says which it
// is, and every use is counted. The match is deliberately loose (a test whose
// own NAME carries the word passes too): fail-open is the direction this file
// owes, since a false deny leaves a session with no way to prove a mutant
// died.
func hasMutationProofMarker(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "mutation") || strings.Contains(lower, "mutant")
}

// decideWholeSuite denies only when the tree already carries a fresh,
// settled verdict this run cannot improve on; with no such verdict on record
// (the common first-run case, or a root the hook cannot resolve) it allows
// silently — an un-narrowed run is legitimate whenever there is nothing to be
// redundant against. The root is the tree the command enters, so a verdict
// about a different checkout is not "such a verdict" at all.
func decideWholeSuite(root string) Decision {
	if root == "" {
		return Decision{Action: Allow}
	}
	entry, fresh := lastFreshSuiteVerdict(root, wholeRunScope())
	if !fresh {
		return Decision{Action: Allow}
	}
	return Decision{
		Action: Block,
		Policy: "bash-whole-suite",
		Reason: denyWholeSuiteReason(root, entry),
	}
}

// denyWholeSuiteReason names where the answer already is, so the caller has
// somewhere to look rather than just a refusal to reword around: the last
// settled gate.log line for this root, and the status verb for the queue
// state that line does not cover.
func denyWholeSuiteReason(root string, e gateEntry) string {
	ago := time.Since(e.at).Round(time.Second)
	return fmt.Sprintf(
		"the gate already holds a %s verdict for %s from the %s stage, logged %s ago — "+
			"re-running the whole suite by hand answers nothing that line does not. See "+
			"`aphrollo gate stats` for the line, or `aphrollo gate status` for this box's "+
			"deferred jobs and build slots. Narrow this to `-run <TestName>`, `-p <crate> <filter>`, or a "+
			"single package if you need a fresh answer for one test.",
		e.verdict, root, e.stage, ago)
}

// hasSoakMarker reports whether the raw command names a soak deliberately —
// the word itself, appearing anywhere (an env var, a flag value, a script
// path, a test name), is the marker a session already reaches for to name
// what it is doing.
func hasSoakMarker(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), "soak")
}

// classifySuiteSegment reads one shellSegments() word list — already
// quote-aware and heredoc-stripped — and reports whether it invokes go
// test / cargo test / cargo nextest run, and if so, whether it names a
// narrowing. Anything else (an alias, a Makefile target, a wrapper script)
// is outside a text classifier's reach and reads as notSuiteInvocation: the
// command may still run a whole suite, but this hook cannot see it, and
// missing one is the fail-open direction it owes.
func classifySuiteSegment(words []string) suiteShape {
	words = dropLeadingEnvAssignments(words)
	switch {
	case len(words) >= 2 && words[0] == "go" && words[1] == "test":
		return classifyRunnerArgs(words[2:], goTestValueFlags, goTestNarrowingFlags)
	case len(words) >= 2 && words[0] == "cargo" && words[1] == "test":
		return classifyRunnerArgs(words[2:], cargoTestValueFlags, cargoTestNarrowingFlags)
	case len(words) >= 3 && words[0] == "cargo" && words[1] == "nextest" && words[2] == "run":
		return classifyRunnerArgs(words[3:], cargoNextestValueFlags, cargoNextestNarrowingFlags)
	default:
		return notSuiteInvocation
	}
}

// wholeSuitePositionalMarkers are the positional operands go test reads as
// "everything", never as a narrowing.
var wholeSuitePositionalMarkers = map[string]bool{"./...": true, "...": true, "./": true}

// goTestValueFlags consume a following bare token as their value (`-run X`,
// not `-run=X`) so it is never mistaken for a positional package operand.
// `-C <dir>` is listed so its operand is consumed as the directory it is,
// never read as a positional package operand (which would classify a whole
// `go test -C <dir> ./...` as narrowed); runnerDir reads the same operand to
// find where the run lands.
var goTestValueFlags = map[string]bool{
	"-run": true, "-timeout": true, "-count": true, "-cpu": true, "-C": true,
}

// goTestNarrowingFlags name go test's own narrowing switch; a single named
// package is recognised separately, as a positional operand.
var goTestNarrowingFlags = map[string]bool{"-run": true}

// `--manifest-path <file>` names the tree, not a narrowing, and its operand
// is consumed for the same reason `-C`'s is: cargo reads any positional as a
// filter substring, so an unconsumed path would read as one.
var cargoTestValueFlags = map[string]bool{"-p": true, "--package": true, "--manifest-path": true}
var cargoTestNarrowingFlags = map[string]bool{"-p": true, "--package": true}

var cargoNextestValueFlags = map[string]bool{
	"-p": true, "--package": true, "-E": true, "--filter-expr": true,
	"--manifest-path": true,
}
var cargoNextestNarrowingFlags = map[string]bool{
	"-p": true, "--package": true, "-E": true, "--filter-expr": true,
}

// classifyRunnerArgs reads a runner's own arguments (the words after `go
// test` / `cargo test` / `cargo nextest run`) and decides whether they name a
// narrowing: an explicit narrowing flag, or a positional operand that is not
// one of go test's own "everything" markers (cargo's positional is always a
// filter substring/expression, so any positional at all narrows there).
func classifyRunnerArgs(args []string, valueFlags, narrowingFlags map[string]bool) suiteShape {
	narrowed := false
	skipNext := false
	for _, w := range args {
		if skipNext {
			skipNext = false
			continue
		}
		name := flagName(w)
		switch {
		case narrowingFlags[name]:
			narrowed = true
			if valueFlags[name] && !strings.Contains(w, "=") {
				skipNext = true
			}
		case valueFlags[name]:
			if !strings.Contains(w, "=") {
				skipNext = true
			}
		case strings.HasPrefix(w, "-"):
			// an ordinary flag, not a narrowing by itself.
		default:
			if !wholeSuitePositionalMarkers[w] {
				narrowed = true
			}
		}
	}
	if narrowed {
		return narrowedSuiteInvocation
	}
	return wholeSuiteInvocation
}

// flagName strips a `--flag=value` token down to `--flag`, so it can be
// looked up in valueFlags/narrowingFlags regardless of which form a command
// used.
func flagName(w string) string {
	if i := strings.IndexByte(w, '='); i >= 0 {
		return w[:i]
	}
	return w
}

// dropLeadingEnvAssignments strips `FOO=bar` prefixes off a segment's words
// (`SOAK_SECS=600 go test ./...`), so the runner name is found at a fixed
// offset regardless of how many precede it.
func dropLeadingEnvAssignments(words []string) []string {
	i := 0
	for i < len(words) && isEnvAssignment(words[i]) {
		i++
	}
	return words[i:]
}

// isEnvAssignment reports whether tok has the shape `NAME=...` with an
// identifier-shaped NAME — the one shell shape this file needs to recognise
// and skip past, not a general assignment parser.
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range tok[:eq] {
		isNameRune := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !isNameRune {
			return false
		}
	}
	return true
}

// suiteStages are the gate.log stages that actually run a project's suite —
// mirrors statsStages, kept as its own set here because this is a membership
// test for freshness, not a display ordering.
var suiteStages = map[string]bool{"postedit": true, "precommit": true, "premergecommit": true}

// lastFreshSuiteVerdict is the most recent SETTLED verdict (see
// isSettledVerdict) logged for root within bashSuiteVerdictFreshFor that is
// AT LEAST AS WIDE as the run being attempted (want) — the answer that rerun
// would be redundant against. Anything else on record for root — a timeout,
// a queued-skipped, a build still deferred, a verdict this reader does not
// recognise — answers nothing, so this reports "no fresh verdict" rather
// than guess: misreading an inconclusive run as settled is what would deny a
// legitimate rerun. So is misreading a NARROW verdict as an answer about the
// whole tree (verdictCoversRun, runscope.go): a post-edit run scoped to one
// module of one package can be green off a single inline test while the
// crate's real tests, in another target entirely, have never run.
func lastFreshSuiteVerdict(root string, want runScope) (gateEntry, bool) {
	e, ok := lastSuiteLogEntry(root, bashSuiteVerdictFreshFor)
	if !ok || !isSettledVerdict(e.verdict) || !verdictCoversRun(e, want) {
		return gateEntry{}, false
	}
	return e, true
}

// lastSuiteLogEntry is the most recent suiteStages entry logged for root
// within window, WHATEVER its verdict — settled or not. Used two ways: to
// build lastFreshSuiteVerdict's stricter answer, and on its own to decide
// whether a narrowed rerun is happening beside RECENT gate activity at all
// (a timeout is exactly the case the narrowed escape exists for, and is not
// itself a "fresh verdict" a whole-suite run could be denied over).
func lastSuiteLogEntry(root string, window time.Duration) (gateEntry, bool) {
	dir := stateDir()
	if dir == "" {
		return gateEntry{}, false
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return gateEntry{}, false
	}
	defer f.Close()
	now := time.Now()
	var best gateEntry
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || !suiteStages[e.stage] || !sameProject(e.root, root) {
			continue
		}
		if now.Sub(e.at) > window {
			continue
		}
		if !found || e.at.After(best.at) {
			best, found = e, true
		}
	}
	return best, found
}

// isSettledVerdict reports whether a gate.log verdict means a suite ran to
// completion and produced an answer — pass, fail, or a law/lint block — as
// opposed to a run that never finished. Matched by explicit prefix rather
// than by excluding a "not settled" list: an unrecognised verdict must read
// as "no fresh answer", never as one.
func isSettledVerdict(v string) bool {
	return v == "green" || strings.HasPrefix(v, "green-") ||
		v == "red" || strings.HasPrefix(v, "red-") ||
		v == "no-delta" || strings.HasSuffix(v, "-blocked")
}

// LogBashSuiteDecision is LogEditDecision's shell-command twin: it records a
// whole-suite invocation the gate denied, and every override an allowed one
// claimed. Keyed on the command's cwd rather than an edited file path, since
// a shell invocation has no file for editTarget to resolve.
func LogBashSuiteDecision(raw []byte, d Decision) {
	if d.Action != Block && len(d.Escapes) == 0 {
		return
	}
	var in bashSuiteInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return
	}
	// Keyed on the tree the command runs in, the same one the decision was
	// judged against — a line blaming the session's cwd would attribute a
	// lane's denied run to the primary checkout.
	root := effectiveRunRoot(in.Cwd, in.ToolInput.Command)
	if root == "" {
		root = in.Cwd
	}
	cmd := logToken(in.ToolInput.Command)
	if d.Action == Block {
		appendGateLog("preedit", logToken(root), cmd, "pretooluse-denied:"+logToken(policyName(d)), 0)
	}
	for _, esc := range d.Escapes {
		appendGateLog("preedit", logToken(root), cmd, logToken(esc), 0)
	}
}
