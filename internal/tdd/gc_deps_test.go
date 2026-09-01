package tdd

import (
	"strings"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// aged writes a file (creating parents) and stamps its mtime.
func aged(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// agedDir creates a directory with one file inside and stamps both.
func agedDir(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	aged(t, filepath.Join(dir, "stamp"), "x", age)
	at := time.Now().Add(-age)
	if err := os.Chtimes(dir, at, at); err != nil {
		t.Fatal(err)
	}
}

// TestGCDeps_TwoTiersByOwnership pins the measured shape of the waste: cargo
// never deletes a SUPERSEDED metadata hash, so `deps/` accumulates one set of
// artifacts per worktree path and per profile change forever (borld: 234
// distinct server-<hash> fingerprints, 207 GB, 106 GB of it exe/pdb untouched
// for more than three days). Workspace members relink in seconds, so they go
// at 3d; third-party artifacts cost a real rebuild, so they wait 14d. Cargo
// rebuilds whatever is missing, which is what makes both safe.
func TestGCDeps_TwoTiersByOwnership(t *testing.T) {
	target := t.TempDir()
	deps := filepath.Join(target, "debug", "deps")
	members := map[string]bool{"server": true}

	staleMember := filepath.Join(deps, "server-0123456789abcdef.exe")
	staleMemberLib := filepath.Join(deps, "libserver-0123456789abcdef.rlib")
	freshMember := filepath.Join(deps, "server-fedcba9876543210.exe")
	youngThirdParty := filepath.Join(deps, "serde-0123456789abcdef.rlib")
	staleThirdParty := filepath.Join(deps, "serde-fedcba9876543210.rlib")
	aged(t, staleMember, "x", 5*24*time.Hour)
	aged(t, staleMemberLib, "x", 5*24*time.Hour)
	aged(t, freshMember, "x", time.Hour)
	aged(t, youngThirdParty, "x", 5*24*time.Hour)
	aged(t, staleThirdParty, "x", 20*24*time.Hour)

	staleFingerprint := filepath.Join(target, "debug", ".fingerprint", "server-0123456789abcdef")
	agedDir(t, staleFingerprint, 5*24*time.Hour)
	staleIncremental := filepath.Join(target, "debug", "incremental", "server-1a2b3c4d5e6f7g8h")
	agedDir(t, staleIncremental, 5*24*time.Hour)

	got := map[string]GCKind{}
	for _, c := range gcDepsArtifacts(target, members, 3*24*time.Hour, 14*24*time.Hour, time.Now()) {
		got[c.Path] = c.Kind
	}

	for _, want := range []string{staleMember, staleMemberLib, staleFingerprint, staleIncremental} {
		if got[want] != GCKindDepsMember {
			t.Errorf("%s: kind %v, want the 3-day member tier", filepath.Base(want), got[want])
		}
	}
	if got[staleThirdParty] != GCKindDepsThirdParty {
		t.Errorf("a 20-day-old third-party artifact must be swept at the 14-day tier")
	}
	if _, proposed := got[freshMember]; proposed {
		t.Error("a fresh member artifact must be kept — it is what the next build links")
	}
	if _, proposed := got[youngThirdParty]; proposed {
		t.Error("a 5-day-old third-party artifact is inside the 14-day tier and must be kept")
	}
}

// TestGCDeps_LeavesUnrecognisedFilesAlone pins the parser's caution: only the
// `<crate>-<16 hex>` shape cargo itself writes qualifies. Anything else in
// deps/ is something this sweep does not understand, and deleting what you do
// not understand is how a sweep eats a build.
func TestGCDeps_LeavesUnrecognisedFilesAlone(t *testing.T) {
	target := t.TempDir()
	deps := filepath.Join(target, "debug", "deps")
	odd := filepath.Join(deps, "notes.txt")
	shortHash := filepath.Join(deps, "server-0123.rlib")
	aged(t, odd, "x", 30*24*time.Hour)
	aged(t, shortHash, "x", 30*24*time.Hour)

	for _, c := range gcDepsArtifacts(target, map[string]bool{"server": true}, 3*24*time.Hour, 14*24*time.Hour, time.Now()) {
		t.Errorf("proposed %s — only cargo's own <crate>-<hash16> shape qualifies", c.Path)
	}
}

// TestGCMutants_OnlyWhenNoRunIsAlive pins category (e): the mutation gate
// copies the whole tree per job under <target>/mutants, and those copies
// outlive the run. Deleting them WHILE cargo-mutants is running would delete
// the tree it is testing, so a live process vetoes the whole category.
func TestGCMutants_OnlyWhenNoRunIsAlive(t *testing.T) {
	target := t.TempDir()
	tree := filepath.Join(target, "mutants", "mutants.out-1")
	agedDir(t, tree, 3*24*time.Hour)
	fresh := filepath.Join(target, "mutants", "mutants.out-2")
	agedDir(t, fresh, time.Hour)

	prev := mutantsRunningFn
	t.Cleanup(func() { mutantsRunningFn = prev })

	mutantsRunningFn = func() bool { return true }
	if got := gcMutantsTrees(target, 24*time.Hour, time.Now()); len(got) != 0 {
		t.Fatalf("proposed %v while cargo-mutants is running", got)
	}

	mutantsRunningFn = func() bool { return false }
	got := gcMutantsTrees(target, 24*time.Hour, time.Now())
	if len(got) != 1 || got[0].Path != tree {
		t.Fatalf("got %v, want just the day-old tree copy %s", got, tree)
	}
}

// TestRenderGC_ReportsPerTierTotals pins what a human needs from the dry
// run once deps sweeping exists: the tiers have different risk, so a reader
// must see how much comes from each rather than one lump sum.
func TestRenderGC_ReportsPerTierTotals(t *testing.T) {
	out := RenderGC([]GCCandidate{
		{Path: "a", Size: 3 << 30, Reason: "member artifact", Kind: GCKindDepsMember},
		{Path: "b", Size: 1 << 30, Reason: "third-party artifact", Kind: GCKindDepsThirdParty},
		{Path: "c", Size: 2 << 30, Reason: "tree copy", Kind: GCKindMutants},
	}, false, 0)
	for _, want := range []string{"workspace artifacts", "third-party artifacts", "mutants trees"} {
		if !containsFold(out, want) {
			t.Fatalf("report is missing the %q tier total:\n%s", want, out)
		}
	}
}

func containsFold(haystack, needle string) bool {
	return len(haystack) >= len(needle) && strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// TestApplyGC_DeletesArtifactsTheDepsTiersProposed pins the rule change item
// 17 made explicit: deps/ is reclaimable through the fingerprint-shaped tiers
// (by cargo's own <crate>-<hash16> stem and an mtime bar), never by name. The
// blanket "never touch deps/" refusal, written when no category understood
// those names, silently refused 44 GB of candidates on the first real run.
func TestApplyGC_DeletesArtifactsTheDepsTiersProposed(t *testing.T) {
	target := t.TempDir()
	artifact := filepath.Join(target, "debug", "deps", "server-0123456789abcdef.rlib")
	aged(t, artifact, "0123456789", 30*24*time.Hour)
	fingerprint := filepath.Join(target, "debug", ".fingerprint", "server-0123456789abcdef")
	agedDir(t, fingerprint, 30*24*time.Hour)

	freed, refused := ApplyGC([]GCCandidate{
		{Path: artifact, Size: 10, Kind: GCKindDepsMember},
		{Path: fingerprint, Size: 1, Kind: GCKindDepsMember},
	})
	if len(refused) != 0 {
		t.Fatalf("refused %v — the deps tiers name their own candidates", refused)
	}
	if freed == 0 {
		t.Fatal("freed nothing")
	}
	for _, p := range []string{artifact, fingerprint} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived", filepath.Base(p))
		}
	}
}

// TestApplyGC_StillRefusesAWholeDepsDirectory pins what the name rule was
// FOR: a category that proposes a whole deps/ (or build/, or .fingerprint/)
// directory is proposing to cold-rebuild the world, and that is still refused.
func TestApplyGC_StillRefusesAWholeDepsDirectory(t *testing.T) {
	target := t.TempDir()
	deps := filepath.Join(target, "debug", "deps")
	aged(t, filepath.Join(deps, "x.rlib"), "x", time.Hour)

	_, refused := ApplyGC([]GCCandidate{{Path: deps, Kind: GCKindIncremental}})
	if len(refused) != 1 {
		t.Fatalf("refused %v, want the whole deps/ directory refused", refused)
	}
	if _, err := os.Stat(deps); err != nil {
		t.Fatal("deps/ must survive")
	}
}
