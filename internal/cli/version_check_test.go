package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	versionPath   = "internal/buildinfo/VERSION"
	changelogPath = "CHANGELOG.md"
	fragmentPath  = "changelog.d/lane-x.md"
	minorFragment = "level: minor\n\nA law now reads comments.\n"
)

const frozenChangelog = "# Changelog\n\nPointer.\n\n## 1.6.6\n\nFrozen words.\n"

// versionLane is a repo with a first commit, and a helper to commit the lane's
// own change on top of it.
type versionLane struct {
	t    *testing.T
	dir  string
	base string
}

func newVersionLane(t *testing.T, baseFiles map[string]string) *versionLane {
	t.Helper()
	dir := gitInit(t, baseFiles)
	return &versionLane{t: t, dir: dir, base: strings.TrimSpace(versionGit(t, dir, "rev-parse", "HEAD"))}
}

func versionGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := fixtureGit(append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (l *versionLane) commit(files map[string]string) {
	l.t.Helper()
	for rel, body := range files {
		writeFile(l.t, filepath.Join(l.dir, filepath.FromSlash(rel)), body)
	}
	versionGit(l.t, l.dir, "add", "-A")
	versionGit(l.t, l.dir, "commit", "-q", "-m", "change")
}

func (l *versionLane) remove(rel string) {
	l.t.Helper()
	versionGit(l.t, l.dir, "rm", "-q", "-f", rel)
	versionGit(l.t, l.dir, "commit", "-q", "-m", "remove")
}

func (l *versionLane) check(body string) (code int, stdout, stderr string) {
	l.t.Helper()
	bodyFile := filepath.Join(l.t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	return runCLI([]string{"version", "check", "--repo", l.dir, "--base", l.base, "--body-file", bodyFile}, "")
}

func TestVersionCheck_AcceptsAMinorPRThatAddsItsOneFragment(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{
		fragmentPath: minorFragment,
		"internal/ratchet/presets/go/new_law.toml": "name = \"x\"\n",
	})

	code, stdout, stderr := lane.check("Adds a law.\n\nversion: minor\n")
	if code != 0 || stdout != "version: ok (minor, "+fragmentPath+")\n" || stderr != "" {
		t.Fatalf("version check = (%d, %q, %q), want (0, \"version: ok (minor, %s)\\n\", \"\")", code, stdout, stderr, fragmentPath)
	}
}

func TestVersionCheck_AcceptsNoneWithNoFragmentForAChangeNoConsumerSees(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{"internal/cli/x.go": "package cli\n"})

	code, stdout, stderr := lane.check("version: none\n")
	if code != 0 || stdout != "version: ok (none)\n" || stderr != "" {
		t.Fatalf("version check = (%d, %q, %q), want (0, \"version: ok (none)\\n\", \"\")", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesAMinorPRThatAddsNoFragment(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{"internal/cli/x.go": "package cli\n"})

	code, stdout, stderr := lane.check("version: minor\n")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "version: the body says `version: minor` but the PR adds no changelog fragment") {
		t.Fatalf("version check = (%d, %q, %q), want exit 1 naming the missing fragment", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesAFragmentWhoseLevelContradictsTheBody(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{fragmentPath: minorFragment})

	code, _, stderr := lane.check("version: patch\n")
	want := "version: " + fragmentPath + " says `level: minor` but the body says `version: patch`: make them agree\n"
	if code != 1 || stderr != want {
		t.Fatalf("version check = (%d, %q), want (1, %q)", code, stderr, want)
	}
}

func TestVersionCheck_RefusesANonePRThatAddsAFragment(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{fragmentPath: minorFragment})

	code, _, stderr := lane.check("version: none\n")
	if code != 1 || !strings.Contains(stderr, "the body says `version: none` but the PR adds "+fragmentPath) {
		t.Fatalf("version check = (%d, %q), want the fragment named", code, stderr)
	}
}

func TestVersionCheck_RefusesAPRThatEditsTheVersionFile(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: frozenChangelog})
	lane.commit(map[string]string{versionPath: "1.1.0\n", fragmentPath: minorFragment})

	code, _, stderr := lane.check("version: minor\n")
	if code != 1 || !strings.Contains(stderr, "version: "+versionPath+" is not edited by a PR") {
		t.Fatalf("version check = (%d, %q), want the VERSION edit refused", code, stderr)
	}
}

// The version file is retired; a PR that removes it is the way out of the old
// rule, not another edit of it.
func TestVersionCheck_AcceptsAPRThatDeletesTheVersionFile(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: frozenChangelog})
	lane.remove(versionPath)

	if code, stdout, stderr := lane.check("version: none\n"); code != 0 {
		t.Fatalf("version check = (%d, %q, %q), want 0", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesAPRThatEditsAReleasedChangelogSection(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{changelogPath: strings.Replace(frozenChangelog, "Frozen words.", "New words.", 1)})

	code, _, stderr := lane.check("version: none\n")
	want := "version: CHANGELOG.md's section for 1.6.6 changed: released sections are frozen, and a later release is written as a changelog.d fragment\n"
	if code != 1 || stderr != want {
		t.Fatalf("version check = (%d, %q), want (1, %q)", code, stderr, want)
	}
}

// A merge that has not been tagged yet is not history: a revert PR deletes the
// fragment of the change it reverts, and must pass.
func TestVersionCheck_AcceptsARevertThatDeletesAFragmentNoTagContainsYet(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog, "changelog.d/old.md": minorFragment})
	versionGit(t, lane.dir, "tag", "v1.7.0")
	lane.commit(map[string]string{"changelog.d/pending.md": minorFragment})
	lane.base = strings.TrimSpace(versionGit(t, lane.dir, "rev-parse", "HEAD"))
	lane.remove("changelog.d/pending.md")

	if code, stdout, stderr := lane.check("version: none\n"); code != 0 {
		t.Fatalf("version check = (%d, %q, %q), want 0", code, stdout, stderr)
	}
}

func TestVersionCheck_AcceptsAPRThatEditsTheChangelogPreamble(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{changelogPath: strings.Replace(frozenChangelog, "Pointer.", "Later releases live in changelog.d.", 1)})

	if code, stdout, stderr := lane.check("version: none\n"); code != 0 {
		t.Fatalf("version check = (%d, %q, %q), want 0", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesAPRThatEditsAFragmentTheNewestTagContains(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog, "changelog.d/old.md": minorFragment})
	versionGit(t, lane.dir, "tag", "v1.7.0")
	lane.commit(map[string]string{"changelog.d/old.md": "level: major\n\nRewritten history.\n"})

	code, _, stderr := lane.check("version: none\n")
	if code != 1 || !strings.Contains(stderr, "changelog.d/old.md is a released fragment") {
		t.Fatalf("version check = (%d, %q), want the old fragment protected", code, stderr)
	}
}

// A branch behind its base is judged on what the branch itself changes: the
// base moved on with its own fragment, and a `version: none` PR that never
// touched changelog.d is not blamed for it (PR #1132).
func TestVersionCheck_JudgesTheBranchFromWhereItForkedNotFromTheMovedBase(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	fork := lane.base
	versionGit(t, lane.dir, "checkout", "-q", "-b", "trunk")
	lane.commit(map[string]string{"changelog.d/other.md": minorFragment})
	trunkTip := strings.TrimSpace(versionGit(t, lane.dir, "rev-parse", "HEAD"))
	versionGit(t, lane.dir, "checkout", "-q", "-b", "pr", fork)
	lane.commit(map[string]string{"README.md": "x\n"})
	lane.base = trunkTip

	code, stdout, stderr := lane.check("Fixes a typo.\n\nversion: none\n")
	if code != 0 || stdout != "version: ok (none)\n" || stderr != "" {
		t.Fatalf("version check = (%d, %q, %q), want (0, \"version: ok (none)\\n\", \"\")", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesABodyWithNoVersionLine(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{"README.md": "x\n"})

	code, stdout, stderr := lane.check("Fixes a typo.\n")
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "version: the PR body has no `version:` line") {
		t.Fatalf("version check = (%d, %q, %q), want exit 1 and the missing line named on stderr", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesALawChangeThatCarriesNoMinorLevel(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{"internal/lang/languages/go.toml": "x = 1\n"})

	code, _, stderr := lane.check("version: none\n")
	if code != 1 || !strings.Contains(stderr, "internal/lang/languages/go.toml changes what a consumer's gate says") {
		t.Fatalf("version check = (%d, %q), want the language row named", code, stderr)
	}
}

func TestVersionCheck_NamesEveryProblemOnItsOwnLine(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: frozenChangelog})
	lane.commit(map[string]string{versionPath: "1.1.0\n", "internal/mask/lex.go": "package mask\n"})

	code, _, stderr := lane.check("version: none\n")
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if code != 1 || len(lines) != 2 {
		t.Fatalf("version check = (%d, %q), want exit 1 and two lines: the VERSION edit and the floor", code, stderr)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "version: ") {
			t.Errorf("line %q does not start with \"version: \"", l)
		}
	}
}

func TestVersionCheck_RefusesBodyLinesThatDisagree(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{"README.md": "x\n"})

	code, _, stderr := lane.check("version: none\nversion: major\n")
	if code != 1 || !strings.HasPrefix(stderr, "version: the PR body's `version:` lines disagree") {
		t.Fatalf("version check = (%d, %q), want the disagreeing lines named", code, stderr)
	}
}

func TestVersionCheck_FailsLoudlyOnABaseItCannotResolve(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.base = "no-such-ref"

	code, stdout, stderr := lane.check("version: none\n")
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "aphrollo version check: ") {
		t.Fatalf("version check = (%d, %q, %q), want exit 1 and an error from the verb", code, stdout, stderr)
	}
}

func TestVersionCheck_FailsLoudlyOutsideARepo(t *testing.T) {
	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("version: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI([]string{"version", "check", "--repo", t.TempDir(), "--base", "HEAD", "--body-file", bodyFile}, "")
	if code != 1 || !strings.Contains(stderr, "is not inside a git repository") {
		t.Fatalf("version check = (%d, %q), want exit 1 naming the missing repository", code, stderr)
	}
}

func TestVersionCheck_FailsLoudlyOnAMissingBodyFile(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	code, _, stderr := runCLI([]string{"version", "check", "--repo", lane.dir, "--base", lane.base, "--body-file", filepath.Join(t.TempDir(), "absent.txt")}, "")
	if code != 1 || !strings.HasPrefix(stderr, "aphrollo version check: ") {
		t.Fatalf("version check = (%d, %q), want exit 1 from the verb", code, stderr)
	}
}

func TestVersionCheck_NeedsABaseAndABodyFile(t *testing.T) {
	for _, args := range [][]string{
		{"version", "check"},
		{"version", "check", "--base", "main"},
		{"version", "check", "--body-file", "b.txt"},
		{"version", "check", "--base", "main", "--body-file", "b.txt", "extra"},
	} {
		code, _, stderr := runCLI(args, "")
		if code != 2 || !strings.Contains(stderr, "usage: aphrollo version check") {
			t.Errorf("%v = (%d, %q), want a usage error (2)", args, code, stderr)
		}
	}
}

func TestVersionCheck_HelpPrintsUsageAndExitsZero(t *testing.T) {
	code, _, stderr := runCLI([]string{"version", "check", "-h"}, "")
	if code != 0 || !strings.Contains(stderr, "usage: aphrollo version check") {
		t.Fatalf("version check -h = (%d, %q), want usage and exit 0", code, stderr)
	}
}

// ratchet: test_removed TestVersionCheck_AcceptsABumpTheBodyDeclaresAndTheChangelogExplains: the PR carries a fragment, not a VERSION bump (TestVersionCheck_AcceptsAMinorPRThatAddsItsOneFragment)
// ratchet: test_removed TestVersionCheck_AcceptsNoBumpForAChangeNoConsumerSees: replaced by TestVersionCheck_AcceptsNoneWithNoFragmentForAChangeNoConsumerSees
// ratchet: test_removed TestVersionCheck_ARepoThatHadNoVersionYetTakesItsFirstOneAsMajor: a repo's first version is the first release tag, never a PR's VERSION file
// ratchet: test_removed TestVersionCheck_RefusesAVersionWhoseChangelogSectionIsMissing: a release's notes are its fragments, and its changelog section is frozen
// ratchet: test_removed TestVersionCheck_RefusesARepoWithNoChangelogAtAll: a release's notes are its fragments; the changelog file is the frozen record
// ratchet: test_removed TestVersionCheck_FailsLoudlyOnAVersionFileItCannotParse: the version file is no longer read
// ratchet: test_removed TestVersionCheck_RefusesALawChangeThatCarriesNoMinorBump: renamed TestVersionCheck_RefusesALawChangeThatCarriesNoMinorLevel

// The directory's own README is documentation, not a fragment: adding or
// changing it asks for no release and is not counted as one.
func TestVersionCheck_TheFragmentDirectorysReadmeIsNotAFragment(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelog})
	lane.commit(map[string]string{"changelog.d/README.md": "# How to write a fragment\n"})

	code, stdout, stderr := lane.check("version: none\n")
	if code != 0 || stdout != "version: ok (none)\n" || stderr != "" {
		t.Fatalf("version check = (%d, %q, %q), want (0, \"version: ok (none)\n\", \"\")", code, stdout, stderr)
	}
}

// ratchet: test_removed TestVersionCheck_RefusesAPRThatEditsAFragmentAlreadyMerged: renamed TestVersionCheck_RefusesAPRThatEditsAFragmentTheNewestTagContains, now that only a fragment the newest tag holds is protected
