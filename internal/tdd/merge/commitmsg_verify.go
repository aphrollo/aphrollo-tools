package merge

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
// A cache-hit precommit run is RESOLVED rather than refused on sight (#591).
// stampGreenSuite's rule — a cache hit "is a fine reason to skip a rerun and a
// poor basis for a claim CI will weigh its own red against" (autoescape.go) —
// is about the CI note, which vouches for one commit to a reader who has no
// access to this box's cache. Here the cache IS available, and the hit can be
// followed back to the run it hit: see cacheHitResolvesGreen. A hit that
// resolves to a green on this exact tree is the same evidence one process
// earlier, and refusing it blocked precisely the mutation-proof record the tdd
// skill requires in the body. A hit that resolves to nothing still refuses.
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
	if headNotedGreenFor(repoRoot, tree) {
		return none, false
	}
	verdict := lastPrecommitVerdict(repoRoot)
	if verdict == mechCacheHitVerdict && cacheHitResolvesGreen(repoRoot) {
		return none, false
	}
	if verdict == "" {
		verdict = "no precommit run recorded"
	}
	AppendGateLog("commitmsg", LogToken(repoRoot), "commit-msg", "commitmsg-rejected:claim", 0)
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"gate commit-msg: this message claims verification, but no green suite ran against the tree being "+
			"committed — the last precommit verdict for this tree is %q.\nRewrite the claim to match what actually ran, then commit again.",
		verdict)}, true
}

// headNotedGreenFor reports whether HEAD's gate note vouches for tree: the
// amend of a commit that keeps its tree (#749). The stamp is consumed by the
// post-commit hook that turned it into the note, so the note is where that
// commit's verdict lives afterwards, and it names the tree it was written
// for — a note on a different tree than the index says nothing here.
func headNotedGreenFor(repoRoot, tree string) bool {
	// tree is never empty here, so an unreadable HEAD ("") never matches it.
	head, _ := revTree(repoRoot, "HEAD")
	return head == tree && commitCarriesGreenGate(repoRoot, "HEAD")
}

// mechCacheHitVerdict is the verdict runSuiteStage logs when it skips a rerun
// because the identical worktree state is already recorded green (mechrun.go).
const mechCacheHitVerdict = "cache-hit"

// cacheHitResolvesGreen follows a "cache-hit" precommit verdict back to the run
// it hit. The mechanical green cache is keyed on (repo, worktree state hash,
// exact command) and records ONLY greens — a red must always re-run, see
// mechcache.go — so an entry under this tree's CURRENT state hash is the
// earlier green run itself, on a tree identical by construction. Any command
// satisfies it: the hit this resolves was logged by the stage that owns the
// suite, and which argv that stage chose is its business, not this guard's.
//
// Nothing recorded for this state resolves to nothing and keeps the refusal:
// a timeout or a deferred run is never cached, and a tree that moved between
// the pre-commit stage and this hook hashes differently, so its cache-hit was
// about some other content.
func cacheHitResolvesGreen(repoRoot string) bool {
	path := mechCachePath()
	if path == "" {
		return false
	}
	green := loadMechCache(path).Green
	for _, root := range cacheHitRoots(repoRoot) {
		hash := worktreeStateHash(root)
		if hash == "" {
			continue
		}
		prefix := mechKeyPrefix(root, hash)
		for key := range green {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
	}
	return false
}

// cacheHitRoots are the roots a cache hit could have been computed at: the
// PROJECT roots stagedRootGroups derived for this commit (FindProjectRoot),
// which is exactly where runSuiteStage hashes and keys.
//
// The repo root is not interchangeable with them. worktreeStateHash is
// cwd-scoped — `git ls-files --others` lists only what sits under the
// directory it runs in — so in a monorepo an untracked scratch file at the
// repo root moves the repo-root hash and leaves the crate's alone, and a
// prefix built at the repo root is one the cache can never hold. repoRoot
// stands in only for a commit that grouped no roots at all, which is a commit
// whose suite stage never ran.
func cacheHitRoots(repoRoot string) []string {
	var roots []string
	for _, g := range stagedRootGroups(repoRoot) {
		roots = append(roots, g.Root)
	}
	if len(roots) == 0 {
		return []string{repoRoot}
	}
	return roots
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
	dir := StateDir()
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
		if !ok || e.Stage != "precommit" || e.Verdict == "ran" || !sameProject(e.Root, root) {
			continue
		}
		last = e.Verdict
	}
	return last
}
