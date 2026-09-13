package tdd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// clampLaw is a law whose hit fixture carries the offence it looks for, so a
// fixtures run over it is green in-process; name it and it is the law a lane
// "changed".
func clampLaw(name string) string {
	return fmt.Sprintf(`
name = %q
description = "A float clamp is not a NaN guard"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`, name)
}

// lawEngineRepo is a checkout of the repo that COMPILES the matchers — the
// only place a lane's own build can exist — carrying one law whose hit
// fixture the running binary cannot produce. That is the #659/#673 shape
// exactly: the row is a fix precisely because this binary rejects it.
func lawEngineRepo(t *testing.T, law string) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "go.mod", "module github.com/aphrollo/aphrollo-tools\n\ngo 1.26.6\n")
	write(t, root, "cmd/aphrollo/main.go", "package main\n\nfunc main() {}\n")
	writeUnprovableLaw(t, root, law)
	gitDo(t, root, "add", ".")
	return root
}

// writeUnprovableLaw lays out a law whose expected hit row the running
// binary's matcher does not produce — the row a matcher correction adds.
func writeUnprovableLaw(t *testing.T, root, law string) {
	t.Helper()
	write(t, root, ".ratchet/laws/"+law+".toml", clampLaw(law))
	write(t, root, ".ratchet/fixtures/"+law+"/hit/crates/a/src/bare.rs", "let a = guarded(x);\n")
	write(t, root, ".ratchet/fixtures/"+law+"/clean/crates/a/src/ok.rs", "let a = guarded(x);\n")
	write(t, root, ".ratchet/fixtures/"+law+"/expected.txt", "crates/a/src/bare.rs:1\n")
}

// writeProvableLaw lays out a law the RUNNING binary proves in both
// directions — the ordinary law a lane has no business having an opinion on.
func writeProvableLaw(t *testing.T, root, law string) {
	t.Helper()
	write(t, root, ".ratchet/laws/"+law+".toml", clampLaw(law))
	write(t, root, ".ratchet/fixtures/"+law+"/hit/crates/a/src/bare.rs", "let a = x.clamp(0.0, 1.0);\n")
	write(t, root, ".ratchet/fixtures/"+law+"/clean/crates/a/src/ok.rs", "let a = guarded(x);\n")
	write(t, root, ".ratchet/fixtures/"+law+"/expected.txt", "crates/a/src/bare.rs:1\n")
}

// stubLaneJudge replaces the build-and-run pair with one that records what it
// was asked for and answers with verdicts, so the stage's decision is tested
// without a toolchain run. asked is the law list the stage handed the lane.
func stubLaneJudge(t *testing.T, answer []ratchet.FixtureResult, asked *[]string) {
	t.Helper()
	oldBuild, oldRun := laneFixtureBuild, laneFixtureRun
	t.Cleanup(func() { laneFixtureBuild, laneFixtureRun = oldBuild, oldRun })
	laneFixtureBuild = func(string) (string, func(), error) {
		return "lane-build", func() {}, nil
	}
	laneFixtureRun = func(_, _ string, laws []string) ([]ratchet.FixtureResult, error) {
		if asked != nil {
			*asked = append([]string{}, laws...)
		}
		return answer, nil
	}
}

// The bootstrap this whole change exists to remove (#659, #673): a lane whose
// change IS a matcher correction stages rows the INSTALLED binary must
// reject, so it can neither commit nor merge until the box binary already
// carries the lane's own change. The lane's build is the only judge that can
// answer for the law the lane is correcting.
func TestRatchetFixtureStage_ProvesALaneChangedLawUnderTheLanesOwnBuild(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "nan-guard")
	stubLaneJudge(t, []ratchet.FixtureResult{{Law: "nan-guard", HitFiles: 1, CleanFiles: 1}}, nil)

	got := ratchetFixtureStage("precommit", root)
	if got.Blocked {
		t.Fatalf("the lane's own build proves the row it stages; stage said:\n%s", got.Message)
	}
}

// The self-grading bound, and the reason the split is safe at all: a lane may
// answer for the laws whose .toml or fixtures it touched and for nothing
// else. A lane build that reports a verdict on a law the commit never
// staged has that verdict DISCARDED — the installed binary judges that law,
// and its refusal stands.
func TestRatchetFixtureStage_DiscardsALaneVerdictForALawTheCommitNeverStaged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "touched")
	// Committed one commit earlier, so it is in the tree and NOT staged now.
	writeUnprovableLaw(t, root, "untouched")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "both laws")
	write(t, root, ".ratchet/laws/touched.toml",
		strings.Replace(clampLaw("touched"), "not a NaN guard", "not a NaN guard, restated", 1))
	gitDo(t, root, "add", ".ratchet/laws/touched.toml")

	stubLaneJudge(t, []ratchet.FixtureResult{
		{Law: "touched", HitFiles: 1, CleanFiles: 1},
		{Law: "untouched", HitFiles: 1, CleanFiles: 1},
	}, nil)

	got := ratchetFixtureStage("precommit", root)
	if !got.Blocked {
		t.Fatal("a lane cannot grade a law it did not touch: untouched's row is unproven and must still block")
	}
	if !strings.Contains(got.Message, "untouched") {
		t.Fatalf("the refusal must name the law the installed binary judged; got:\n%s", got.Message)
	}
}

// The bound cuts BOTH ways, and this is the half that is load-bearing on its
// own: a verdict the lane returns for a law the commit never staged is
// discarded whatever it says, so a lane build cannot REFUSE a law it did not
// touch either. Without the discard a lane's binary could block a commit over
// a law whose fixtures are sound under the judge that actually owns them —
// the same overreach as grading its own homework, pointed the other way.
func TestRatchetFixtureStage_DiscardsALaneREFUSALForALawTheCommitNeverStaged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "touched")
	writeProvableLaw(t, root, "untouched")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "both laws")
	write(t, root, ".ratchet/fixtures/touched/clean/crates/a/src/more.rs", "let a = guarded(x);\n")
	gitDo(t, root, "add", ".ratchet/fixtures/touched")

	stubLaneJudge(t, []ratchet.FixtureResult{
		{Law: "touched", HitFiles: 1, CleanFiles: 2},
		{Law: "untouched", Failures: []string{"a law this lane never touched"}},
	}, nil)

	got := ratchetFixtureStage("precommit", root)
	if got.Blocked {
		t.Fatalf("untouched belongs to the installed binary, which proved it; stage said:\n%s", got.Message)
	}
}

// The same bound stated on the way IN: the lane is only ever ASKED about the
// laws the commit stages, so a law it did not touch is never even offered to
// it.
func TestRatchetFixtureStage_AsksTheLaneOnlyAboutTheLawsTheCommitStages(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "touched")
	writeUnprovableLaw(t, root, "untouched")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "both laws")
	write(t, root, ".ratchet/fixtures/touched/clean/crates/a/src/more.rs", "let a = guarded(x);\n")
	gitDo(t, root, "add", ".ratchet/fixtures/touched")

	var asked []string
	stubLaneJudge(t, []ratchet.FixtureResult{{Law: "touched", HitFiles: 1, CleanFiles: 2}}, &asked)

	ratchetFixtureStage("precommit", root)
	if len(asked) != 1 || asked[0] != "touched" {
		t.Fatalf("the lane may answer only for the laws it staged; it was asked for %v", asked)
	}
}

// A lane that cannot be built has proved nothing, and the row it stages is
// exactly the one nothing else can judge. Letting the commit through on a
// build failure would reinstate the hole in its worst form: unprovable rows
// landing unproven.
func TestRatchetFixtureStage_BlocksWhenTheLanesOwnBuildFails(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "nan-guard")
	oldBuild := laneFixtureBuild
	t.Cleanup(func() { laneFixtureBuild = oldBuild })
	laneFixtureBuild = func(string) (string, func(), error) {
		return "", func() {}, fmt.Errorf("go build: undefined: carriesTrigger")
	}

	got := ratchetFixtureStage("precommit", root)
	if !got.Blocked {
		t.Fatal("a lane that does not compile proves nothing; the commit must be refused")
	}
	if !strings.Contains(got.Message, "carriesTrigger") {
		t.Fatalf("the refusal must carry the build's own reason; got:\n%s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "check-error-rejected")
}

// A lane build that answers for fewer laws than it was asked about leaves
// those laws with no verdict from ANY judge. An absent verdict is not a
// clean one.
func TestRatchetFixtureStage_BlocksWhenTheLaneAnswersForFewerLawsThanItWasAsked(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "nan-guard")
	stubLaneJudge(t, nil, nil)

	got := ratchetFixtureStage("precommit", root)
	if !got.Blocked {
		t.Fatal("no verdict is not a clean verdict; the commit must be refused")
	}
	if !strings.Contains(got.Message, "nan-guard") {
		t.Fatalf("the refusal must name the unanswered law; got:\n%s", got.Message)
	}
}

// A CONSUMING repo has no matcher source to build: its laws are data judged
// by the installed binary, and there is no lane build that could differ. It
// must never pay for one, and the installed binary's refusal stands.
func TestRatchetFixtureStage_NeverBuildsALaneInARepoThatDoesNotCompileTheMatchers(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	writeUnprovableLaw(t, root, "nan-guard")
	gitDo(t, root, "add", ".")

	oldBuild := laneFixtureBuild
	t.Cleanup(func() { laneFixtureBuild = oldBuild })
	built := false
	laneFixtureBuild = func(string) (string, func(), error) {
		built = true
		return "", func() {}, fmt.Errorf("no lane build here")
	}

	got := ratchetFixtureStage("precommit", root)
	if built {
		t.Fatal("a repo that does not compile the matchers has no lane build to run")
	}
	if !got.Blocked {
		t.Fatal("the installed binary still judges a consuming repo's laws")
	}
}

// A staged path under .ratchet/fixtures/ names a law by its DIRECTORY, which
// the tree need not have a law for: fixtures written before their law, or a
// directory whose name does not match one. Asking the lane for a verdict on a
// name no law owns refuses the commit over a law nobody could ever judge, so
// the ask is intersected with the laws the tree actually has.
func TestRatchetFixtureStage_AsksForNoVerdictOnAFixturePathNoLawOwns(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawEngineRepo(t, "nan-guard")
	write(t, root, ".ratchet/fixtures/nan-guard/hit/crates/a/src/bare.rs", "let a = x.clamp(0.0, 1.0);\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "law")
	write(t, root, ".ratchet/fixtures/typo-guard/hit/crates/a/src/bare.rs", "let a = x.clamp(0.0, 1.0);\n")
	gitDo(t, root, "add", ".ratchet/fixtures/typo-guard")

	var asked []string
	stubLaneJudge(t, nil, &asked)

	got := ratchetFixtureStage("precommit", root)
	if len(asked) != 0 {
		t.Fatalf("no law owns typo-guard, so nothing can judge it; the lane was asked for %v", asked)
	}
	if got.Blocked {
		t.Fatalf("a fixture directory no law owns is not an unproven law; stage said:\n%s", got.Message)
	}
}
