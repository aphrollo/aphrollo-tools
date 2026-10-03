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
)

func changelogWith(versions ...string) string {
	var b strings.Builder
	b.WriteString("# Changelog\n\n")
	for _, v := range versions {
		b.WriteString("## " + v + "\n\nWhat a consumer will notice.\n\n")
	}
	return b.String()
}

// versionLane is a repo whose first commit carries version base, and a helper
// to commit the lane's own change on top of it.
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

func (l *versionLane) check(body string) (code int, stdout, stderr string) {
	l.t.Helper()
	bodyFile := filepath.Join(l.t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	return runCLI([]string{"version", "check", "--repo", l.dir, "--base", l.base, "--body-file", bodyFile}, "")
}

func TestVersionCheck_AcceptsABumpTheBodyDeclaresAndTheChangelogExplains(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	lane.commit(map[string]string{
		versionPath:   "1.1.0\n",
		changelogPath: changelogWith("1.1.0", "1.0.0"),
		"internal/ratchet/presets/go/new_law.toml": "name = \"x\"\n",
	})

	code, stdout, stderr := lane.check("Adds a law.\n\nversion: minor\n")
	if code != 0 || stdout != "version: ok (1.0.0 -> 1.1.0)\n" || stderr != "" {
		t.Fatalf("version check = (%d, %q, %q), want (0, \"version: ok (1.0.0 -> 1.1.0)\\n\", \"\")", code, stdout, stderr)
	}
}

func TestVersionCheck_AcceptsNoBumpForAChangeNoConsumerSees(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.2.3\n", changelogPath: changelogWith("1.2.3")})
	lane.commit(map[string]string{"internal/cli/x.go": "package cli\n"})

	if code, stdout, stderr := lane.check("version: none\n"); code != 0 {
		t.Fatalf("version check = (%d, %q, %q), want 0", code, stdout, stderr)
	}
}

func TestVersionCheck_ARepoThatHadNoVersionYetTakesItsFirstOneAsMajor(t *testing.T) {
	lane := newVersionLane(t, map[string]string{"README.md": "x\n"})
	lane.commit(map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})

	if code, stdout, stderr := lane.check("version: major\n"); code != 0 {
		t.Fatalf("version check = (%d, %q, %q), want 0", code, stdout, stderr)
	}
	code, _, stderr := lane.check("version: minor\n")
	if code != 1 || !strings.Contains(stderr, "version: the body says `version: minor` but VERSION went 0.0.0 -> 1.0.0, which is major") {
		t.Fatalf("a first version declared minor = (%d, %q), want it refused as a major", code, stderr)
	}
}

// A branch behind its base is judged on what the branch itself changes: the
// base moved on with its own bump (1.0.0 -> 1.0.1), and a `version: none` PR
// that never touched VERSION is not blamed for it (PR #1132).
func TestVersionCheck_JudgesTheBranchFromWhereItForkedNotFromTheMovedBase(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	fork := lane.base
	versionGit(t, lane.dir, "checkout", "-q", "-b", "trunk")
	lane.commit(map[string]string{versionPath: "1.0.1\n", changelogPath: changelogWith("1.0.1", "1.0.0")})
	trunkTip := strings.TrimSpace(versionGit(t, lane.dir, "rev-parse", "HEAD"))
	versionGit(t, lane.dir, "checkout", "-q", "-b", "pr", fork)
	lane.commit(map[string]string{"README.md": "x\n"})
	lane.base = trunkTip

	code, stdout, stderr := lane.check("Fixes a typo.\n\nversion: none\n")
	if code != 0 || stdout != "version: ok (1.0.0 -> 1.0.0)\n" || stderr != "" {
		t.Fatalf("version check = (%d, %q, %q), want (0, \"version: ok (1.0.0 -> 1.0.0)\\n\", \"\")", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesABodyWithNoVersionLine(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	lane.commit(map[string]string{"README.md": "x\n"})

	code, stdout, stderr := lane.check("Fixes a typo.\n")
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "version: the PR body has no `version:` line") {
		t.Fatalf("version check = (%d, %q, %q), want exit 1 and the missing line named on stderr", code, stdout, stderr)
	}
}

func TestVersionCheck_RefusesALawChangeThatCarriesNoMinorBump(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	lane.commit(map[string]string{"internal/lang/languages/go.toml": "x = 1\n"})

	code, _, stderr := lane.check("version: none\n")
	if code != 1 || !strings.Contains(stderr, "internal/lang/languages/go.toml changes what a consumer's gate says") {
		t.Fatalf("version check = (%d, %q), want the language row named", code, stderr)
	}
}

func TestVersionCheck_RefusesAVersionWhoseChangelogSectionIsMissing(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	lane.commit(map[string]string{versionPath: "1.1.0\n"})

	code, _, stderr := lane.check("version: minor\n")
	want := "version: CHANGELOG.md has no section for 1.1.0: add a `## 1.1.0` heading with what a consumer will notice and what migrates by itself\n"
	if code != 1 || stderr != want {
		t.Fatalf("version check = (%d, %q), want (1, %q)", code, stderr, want)
	}
}

func TestVersionCheck_RefusesARepoWithNoChangelogAtAll(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n"})
	lane.commit(map[string]string{versionPath: "1.1.0\n"})

	code, _, stderr := lane.check("version: minor\n")
	if code != 1 || !strings.Contains(stderr, "CHANGELOG.md has no section for 1.1.0") {
		t.Fatalf("version check = (%d, %q), want the missing changelog named", code, stderr)
	}
}

func TestVersionCheck_NamesEveryProblemOnItsOwnLine(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	lane.commit(map[string]string{"internal/mask/lex.go": "package mask\n"})

	code, _, stderr := lane.check("nothing\n")
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if code != 1 || len(lines) != 2 {
		t.Fatalf("version check = (%d, %q), want exit 1 and two lines: the missing line and the floor", code, stderr)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "version: ") {
			t.Errorf("line %q does not start with \"version: \"", l)
		}
	}
}

func TestVersionCheck_FailsLoudlyOnABaseItCannotResolve(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
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

func TestVersionCheck_FailsLoudlyOnAVersionFileItCannotParse(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
	lane.commit(map[string]string{versionPath: "one point one\n"})

	code, _, stderr := lane.check("version: minor\n")
	if code != 1 || !strings.Contains(stderr, "internal/buildinfo/VERSION") || !strings.Contains(stderr, "one point one") {
		t.Fatalf("version check = (%d, %q), want the unreadable VERSION named", code, stderr)
	}
}

func TestVersionCheck_FailsLoudlyOnAMissingBodyFile(t *testing.T) {
	lane := newVersionLane(t, map[string]string{versionPath: "1.0.0\n", changelogPath: changelogWith("1.0.0")})
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
