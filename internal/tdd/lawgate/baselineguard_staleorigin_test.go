package lawgate

import (
	"path/filepath"
	"strings"
	"testing"
)

const staleOriginBaselineRel = ".ratchet/baselines/module_size.txt"

// staleOriginModules are the modules main adds in its unpushed range: none of
// them exists at `origin/main`, and exactly ONE of them carries a module_size
// baseline row. That ratio is the measured shape of the repo this came from —
// 7 of the lane's 15 changed files absent at `origin/main`, 1 of those 7 with
// a row — and it is what makes the assertions below discriminating: under the
// defect the guard names precisely one file, because only one of the absent
// files has a row to read as a regression from nothing.
var staleOriginModules = []string{
	"crates/x/tests/gjk_epa_differential.rs",
	"crates/x/tests/narrowphase.rs",
	"crates/x/src/broadphase.rs",
}

// staleOriginRowed is the one of those modules with a baseline row, at the
// size the ratchet itself measured when main added it.
const staleOriginRowed = "crates/x/tests/gjk_epa_differential.rs"

// staleOriginFixture builds a real repository whose `origin/main` is genuinely
// STALE: the branch is pushed to a real bare remote once, and local `main`
// then advances over several commits past that publication point without ever
// pushing again. The unpushed range adds staleOriginModules AND the baseline
// row the ratchet wrote for the one that needed it, and a lane branches off
// that unpushed tip carrying no baseline change at all.
//
// The remote-tracking ref has to be real and the pushes have to be real,
// because the defect lives entirely in WHICH ref the base resolves to: a test
// that hands the resolver its answer proves nothing about the resolver.
func staleOriginFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "seed.txt"), "seed\n")
	mustWrite(t, filepath.Join(root, filepath.FromSlash(staleOriginBaselineRel)),
		"# module size\ncrates/x/src/lib.rs | 10\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitFixture(t, root, "branch", "-M", "main")

	// The one and only publication: origin/main is pinned here forever after.
	remote := filepath.Join(t.TempDir(), "origin.git")
	gitFixture(t, root, "init", "--bare", remote)
	gitFixture(t, root, "remote", "add", "origin", remote)
	gitFixture(t, root, "push", "-q", "origin", "main")

	// Main moves on without pushing, one commit per module, and writes the row
	// for the one that needed it. Every one of them is in the unpushed range,
	// so against origin/main they read as brand new and against main they are
	// old news.
	for _, mod := range staleOriginModules {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(mod)), "fn m() {}\n")
		if mod == staleOriginRowed {
			mustWrite(t, filepath.Join(root, filepath.FromSlash(staleOriginBaselineRel)),
				"# module size\ncrates/x/src/lib.rs | 10\n"+mod+" | 2006\n")
		}
		gitAddAll(t, root)
		commitAll(t, root)
	}

	assertStaleOriginShape(t, root)
	gitFixture(t, root, "checkout", "-q", "-b", "lane/work")
	return root
}

// assertStaleOriginShape runs the reporter's own diagnostic against the
// fixture, so a fixture that quietly stops being stale — a push that creeps
// into the setup, a default-branch rename, a git that auto-updates the
// tracking ref — fails as a broken fixture instead of passing vacuously.
func assertStaleOriginShape(t *testing.T, root string) {
	t.Helper()
	for _, mod := range staleOriginModules {
		if _, err := git(root, "cat-file", "-e", "origin/main:"+mod); err == nil {
			t.Fatalf("setup: %s must NOT exist at the stale origin/main", mod)
		}
		if _, err := git(root, "cat-file", "-e", "HEAD:"+mod); err != nil {
			t.Fatalf("setup: %s must exist at HEAD: %v", mod, err)
		}
	}
	at := func(ref string) string {
		text, ok := gitBlob(root, ref+":"+staleOriginBaselineRel)
		if !ok {
			t.Fatalf("setup: no baseline at %s", ref)
		}
		return text
	}
	if strings.Contains(at("origin/main"), staleOriginRowed) {
		t.Fatal("setup: the stale origin/main must not carry the row main added later")
	}
	if !strings.Contains(at("HEAD"), staleOriginRowed) {
		t.Fatal("setup: HEAD must carry the row main added in the unpushed range")
	}
}

// baselineOffences lists the offences a rejection message names, one per
// `baseline-rejected:` occurrence — the count and identity the assertions
// here turn on, rather than the bare fact that something was refused.
func baselineOffences(msg string) []string {
	var out []string
	for line := range strings.Lines(msg) {
		if _, rest, ok := strings.Cut(line, "baseline-rejected: "); ok {
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

// TestBaselineGuard_DoesNotChargeALaneForARowOnlyAStaleOriginCallsNew: a repo
// whose local main is hundreds of commits ahead of its last push could not
// commit at all. The base was resolved as `origin/main` — a PUBLICATION point,
// not a base — so main's own unpushed commits counted as the lane's work and a
// row main itself wrote days ago read as this lane raising a ceiling:
//
//	gate precommit: baseline-rejected: .ratchet/baselines/module_size.txt
//	  crates/.../gjk_epa_differential.rs 0 -> 2006
//
// on a lane that touched no baseline. `0 -> N` is the signature: absent at the
// base that was consulted, N now. Exactly one file is named under the defect —
// the one absent module that has a row — so zero named files is the assertion,
// and it separates the fixed guard from the broken one.
func TestBaselineGuard_DoesNotChargeALaneForARowOnlyAStaleOriginCallsNew(t *testing.T) {
	root := staleOriginFixture(t)

	mustWrite(t, filepath.Join(root, "notes.txt"), "unrelated\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if offences := baselineOffences(res.Message); len(offences) != 0 {
		t.Fatalf("a row local trunk already carries is not this lane's raise; named %d: %v", len(offences), offences)
	}
	if res.Blocked {
		t.Fatalf("the lane touched no baseline and must not be blocked: %s", res.Message)
	}
}

// ...and #206's catch survives the same fixture: a raise landed by an EARLIER
// commit in the lane is already in HEAD, so a comparison against HEAD alone
// finds nothing while the merge gate rejects the lane. The base stays the
// lane's branch point — now the local trunk one rather than the stale remote
// one — so the raise is still seen on every later commit, named once and
// measured against what main actually says (2006), not against nothing.
func TestBaselineGuard_StillSeesALaneEarlierRaiseWhenOriginIsStale(t *testing.T) {
	root := staleOriginFixture(t)

	// The lane's first commit hand-raises the row main wrote.
	mustWrite(t, filepath.Join(root, filepath.FromSlash(staleOriginBaselineRel)),
		"# module size\ncrates/x/src/lib.rs | 10\n"+staleOriginRowed+" | 2007\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// The commit being gated touches something else entirely.
	mustWrite(t, filepath.Join(root, "notes.txt"), "unrelated\n")
	gitAddAll(t, root)

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a raise an earlier commit in the lane landed must stay visible, or the merge gate is the first to see it")
	}
	offences := baselineOffences(res.Message)
	if len(offences) != 1 {
		t.Fatalf("exactly one row was raised, so exactly one offence: %v", offences)
	}
	if !strings.Contains(offences[0], staleOriginRowed) {
		t.Errorf("the offence must name the raised module: %s", offences[0])
	}
	if !strings.Contains(offences[0], "2006 -> 2007") {
		t.Errorf("message must name the raise against local trunk (2006 -> 2007): %s", offences[0])
	}
}

// TestLaneChangedPaths_ExcludesTrunksOwnUnpushedWork is the larger half of the
// same defect. The lane's file set was taken against the same publication
// point, so in a repo with a stale origin MAIN'S OWN unpushed commits counted
// as the lane's work: the format check charged the lane for trunk's files, and
// the baseline guard judged rows trunk itself had written. A lane's diff is
// what it will carry INTO trunk, which can never include trunk's own commits.
func TestLaneChangedPaths_ExcludesTrunksOwnUnpushedWork(t *testing.T) {
	root := staleOriginFixture(t)

	mustWrite(t, filepath.Join(root, "lane.go"), "package lane\n")
	gitAddAll(t, root)
	commitAll(t, root)

	got := laneChangedPaths(root)
	want := []string{"lane.go"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("lane diff must be the lane's own work only: got %v, want %v", got, want)
	}
}
