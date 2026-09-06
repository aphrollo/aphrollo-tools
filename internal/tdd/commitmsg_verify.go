package tdd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// verificationClaimPattern flags a commit-msg BODY that claims the change
// was actually tested. The words alone are a weak signal — a survey of 1142
// commits in this repo's own history found the phrase on 69 (6%), almost all
// honest ("Verified locally: go test -run ...", "Verified under CI's own
// flags before merge") — so verificationClaimCheck never blocks on the text
// alone. It also asks whether a real green suite ran against the exact tree
// this commit is about to create.
var verificationClaimPattern = regexp.MustCompile(
	`(?i)\btests? pass(es)?\b|\ball green\b|\bverified\b|\bconfirmed working\b|\bsuite passes\b|\bno regressions\b`)

// verificationClaimCheck rejects a commit-msg body claiming verification
// with no fresh green suite behind the tree being committed.
// stampGreenSuite/StampGreenSuiteIfProven (autoescape.go) already compute
// the one fact this needs — the staged tree a suite in THIS pre-commit run
// actually went green on — for the CI git-note channel; this reads that same
// stamp, non-destructively (PostCommit is still the only consumer that
// deletes it), rather than inventing a second notion of "proven".
//
// A cache-hit precommit run (the identical tree already proven earlier)
// deliberately does not satisfy this either, matching stampGreenSuite's own
// rule: a cache hit "is a fine reason to skip a rerun and a poor basis for a
// claim CI will weigh its own red against" (autoescape.go) — the same
// reasoning applies to a claim written into the message itself.
//
// A repoRoot outside a real git work tree (indexTree returns "") fails OPEN:
// this gate protects a convention, not correctness, and must never wedge a
// commit over a broken or absent git.
func verificationClaimCheck(repoRoot, body string) (GateResult, bool) {
	var none GateResult
	if !verificationClaimPattern.MatchString(body) {
		return none, false
	}
	tree := indexTree(repoRoot)
	if tree == "" {
		return none, false
	}
	if stamped, ok := readCurrentGreenSuiteStamp(repoRoot); ok && stamped == tree {
		return none, false
	}
	verdict := lastPrecommitVerdict(repoRoot)
	if verdict == "" {
		verdict = "no precommit run recorded"
	}
	appendGateLog("commitmsg", logToken(repoRoot), "commit-msg", "commitmsg-rejected:claim", 0)
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate commit-msg: this message claims verification, but no green suite ran against the tree being "+
			"committed — the last precommit verdict for this tree is %q.\nRewrite the claim to match what actually ran, then commit again.",
		verdict)}, true
}

// readCurrentGreenSuiteStamp reads the tree stampGreenSuite last recorded for
// repoRoot, WITHOUT consuming it — PostCommit remains the only consumer that
// deletes the file, so a commit-msg check here must never interfere with the
// git-note channel reading the same stamp moments later in the same commit.
func readCurrentGreenSuiteStamp(repoRoot string) (string, bool) {
	path := greenSuiteStampFile(repoRoot)
	if len(path) == 0 {
		// absence-ok: an unreadable stamp is not proof of a green suite; both cases refuse the claim
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: an unreadable stamp is not proof of a green suite; both cases refuse the claim
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// lastPrecommitVerdict returns the most recent precommit-stage verdict
// gate.log recorded for root, skipping Precommit's own unconditional "ran"
// marker (precommitmarker.go) — that marker exists to prove the gate fired
// at all, not to say what it found, so surfacing it here would quote "ran"
// back at every rejection regardless of what actually happened.
func lastPrecommitVerdict(root string) string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return ""
	}
	defer f.Close()
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || e.stage != "precommit" || e.verdict == "ran" || !sameProject(e.root, root) {
			continue
		}
		last = e.verdict
	}
	return last
}
