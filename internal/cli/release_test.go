package cli

import (
	"regexp"
	"strings"
	"testing"
)

const frozenChangelogWithDate = "# Changelog\n\nWhat changes for a consumer.\n\n## 1.6.6 - 2026-10-03\n\nFrozen words.\n\n## 1.6.5\n\nOlder words.\n"

// frozenRepo is a lane-less repo whose first commit is the frozen v1.6.6.
func frozenRepo(t *testing.T) *versionLane {
	t.Helper()
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelogWithDate})
	versionGit(t, lane.dir, "tag", "v1.6.6")
	return lane
}

func (l *versionLane) run(args ...string) (code int, stdout, stderr string) {
	l.t.Helper()
	return runCLI(append(args, "--repo", l.dir), "")
}

func TestReleasePlan_NamesTheTagTheNewFragmentsEarn(t *testing.T) {
	lane := frozenRepo(t)
	lane.commit(map[string]string{"changelog.d/a-fix.md": "level: patch\n\nFixes a typo.\n"})
	lane.commit(map[string]string{"changelog.d/b-feature.md": "level: minor\n\nA law now reads comments.\n"})

	code, stdout, stderr := lane.run("release", "plan")
	if code != 0 || stdout != "v1.7.0\n" {
		t.Fatalf("release plan = (%d, %q, %q), want (0, \"v1.7.0\\n\", ...)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "v1.7.0 (minor): a-fix, b-feature") {
		t.Errorf("stderr = %q, want it to say why: the level and the fragments", stderr)
	}
}

// A re-run of the release job, and the next push after a tag, must find the
// tag already made and make nothing.
func TestReleasePlan_ASecondRunAfterTheTagIsMadeFindsNothingToDo(t *testing.T) {
	lane := frozenRepo(t)
	lane.commit(map[string]string{"changelog.d/a-feature.md": "level: minor\n\nA law now reads comments.\n"})
	versionGit(t, lane.dir, "tag", "v1.7.0")

	code, stdout, stderr := lane.run("release", "plan")
	if code != 0 || stdout != "" || !strings.Contains(stderr, "nothing to tag") {
		t.Fatalf("release plan = (%d, %q, %q), want (0, \"\", a nothing-to-tag line)", code, stdout, stderr)
	}
}

func TestReleasePlan_ACommitWithNoFragmentMintsNothing(t *testing.T) {
	lane := frozenRepo(t)
	lane.commit(map[string]string{"internal/cli/x.go": "package cli\n"})

	code, stdout, _ := lane.run("release", "plan")
	if code != 0 || stdout != "" {
		t.Fatalf("release plan = (%d, %q), want nothing to tag", code, stdout)
	}
}

// The workflow tags the commit that triggered it; a fragment that landed on
// main after that commit belongs to the next push, not to this tag.
func TestReleasePlan_ReadsTheFragmentsOfTheRevItIsAskedAbout(t *testing.T) {
	lane := frozenRepo(t)
	lane.commit(map[string]string{"changelog.d/a-fix.md": "level: patch\n\nFixes a typo.\n"})
	first := strings.TrimSpace(versionGit(t, lane.dir, "rev-parse", "HEAD"))
	lane.commit(map[string]string{"changelog.d/b-feature.md": "level: minor\n\nA law now reads comments.\n"})

	code, stdout, stderr := lane.run("release", "plan", "--rev", first)
	if code != 0 || stdout != "v1.6.7\n" {
		t.Fatalf("release plan --rev = (%d, %q, %q), want v1.6.7: the later minor fragment is not in that tree", code, stdout, stderr)
	}
}

func TestReleasePlan_RefusesWhenTheNewestTagIsNotBehindTheRev(t *testing.T) {
	lane := frozenRepo(t)
	versionGit(t, lane.dir, "checkout", "-q", "-b", "side")
	lane.commit(map[string]string{"changelog.d/a.md": "level: minor\n\nWords.\n"})
	versionGit(t, lane.dir, "tag", "v1.7.0")
	versionGit(t, lane.dir, "checkout", "-q", "-b", "other", "v1.6.6")
	lane.commit(map[string]string{"changelog.d/b.md": "level: minor\n\nOther words.\n"})

	code, stdout, stderr := lane.run("release", "plan")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "v1.7.0 is not an ancestor of HEAD") {
		t.Fatalf("release plan = (%d, %q, %q), want a refusal naming v1.7.0 and HEAD", code, stdout, stderr)
	}
}

func TestReleasePlan_RefusesAFragmentItCannotReadRatherThanSkippingIt(t *testing.T) {
	lane := frozenRepo(t)
	lane.commit(map[string]string{"changelog.d/broken.md": "Words but no level.\n"})

	code, stdout, stderr := lane.run("release", "plan")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "changelog.d/broken.md") {
		t.Fatalf("release plan = (%d, %q, %q), want the broken fragment named", code, stdout, stderr)
	}
}

func TestReleasePlan_NeedsAReleaseTagToBumpFrom(t *testing.T) {
	lane := newVersionLane(t, map[string]string{changelogPath: frozenChangelogWithDate})
	lane.commit(map[string]string{"changelog.d/a.md": "level: minor\n\nWords.\n"})

	code, _, stderr := lane.run("release", "plan")
	if code != 1 || !strings.Contains(stderr, "no release tag") {
		t.Fatalf("release plan = (%d, %q), want a refusal naming the missing tag", code, stderr)
	}
}

func TestRelease_UsageErrorsAreTwo(t *testing.T) {
	for _, args := range [][]string{{"release"}, {"release", "bogus"}, {"release", "plan", "extra"}, {"release", "plan", "--no-such-flag"}} {
		if code, _, _ := runCLI(args, ""); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
	}
	if code, stdout, _ := runCLI([]string{"release", "--help"}, ""); code != 0 || !strings.Contains(stdout, "usage: aphrollo release plan") {
		t.Errorf("release --help = (%d, %q), want usage and 0", code, stdout)
	}
}

// historyRepo is the frozen v1.6.6, a patch release, a minor release and one
// fragment no tag holds yet.
func historyRepo(t *testing.T) *versionLane {
	t.Helper()
	lane := frozenRepo(t)
	lane.commit(map[string]string{"changelog.d/a-fix.md": "level: patch\n\nFixes a typo.\n"})
	versionGit(t, lane.dir, "tag", "v1.6.7")
	lane.commit(map[string]string{"changelog.d/b-feature.md": "level: minor\n\nA law now reads comments.\n\n- It flags one more thing.\n"})
	versionGit(t, lane.dir, "tag", "v1.7.0")
	lane.commit(map[string]string{"changelog.d/c-next.md": "level: patch\n\nNot released yet.\n"})
	return lane
}

func TestChangelog_AssemblesTheFullHistoryNewestFirst(t *testing.T) {
	lane := historyRepo(t)

	code, stdout, stderr := lane.run("changelog")
	if code != 0 || stderr != "" {
		t.Fatalf("changelog = (%d, %q, %q), want 0 and a quiet stderr", code, stdout, stderr)
	}
	dated := regexp.MustCompile(`## (1\.7\.0|1\.6\.7) - \d{4}-\d{2}-\d{2}`)
	got := dated.ReplaceAllString(stdout, "## $1 - DATE")
	want := "# Changelog\n\nWhat changes for a consumer.\n\n" +
		"## Unreleased\n\nNot released yet.\n\n" +
		"## 1.7.0 - DATE\n\nA law now reads comments.\n\n- It flags one more thing.\n\n" +
		"## 1.6.7 - DATE\n\nFixes a typo.\n\n" +
		"## 1.6.6 - 2026-10-03\n\nFrozen words.\n\n## 1.6.5\n\nOlder words.\n"
	if got != want {
		t.Fatalf("changelog =\n%s\nwant\n%s", got, want)
	}
}

func TestChangelog_APlainRepoIsItsFrozenFileUntouched(t *testing.T) {
	lane := frozenRepo(t)
	code, stdout, _ := lane.run("changelog")
	if code != 0 || stdout != frozenChangelogWithDate {
		t.Fatalf("changelog = (%d, %q), want the frozen file byte for byte", code, stdout)
	}
}

func TestChangelog_ATagsNotesAreTheFragmentsItWasFirstToContain(t *testing.T) {
	lane := historyRepo(t)

	code, stdout, stderr := lane.run("changelog", "--tag", "v1.7.0")
	if want := "A law now reads comments.\n\n- It flags one more thing.\n"; code != 0 || stdout != want {
		t.Fatalf("changelog --tag v1.7.0 = (%d, %q, %q), want (0, %q)", code, stdout, stderr, want)
	}
	code, stdout, _ = lane.run("changelog", "--tag", "v1.6.7")
	if want := "Fixes a typo.\n"; code != 0 || stdout != want {
		t.Fatalf("changelog --tag v1.6.7 = (%d, %q), want (0, %q): the later fragments are not its notes", code, stdout, want)
	}
}

func TestChangelog_AFrozenTagsNotesAreItsSectionInTheChangelog(t *testing.T) {
	lane := historyRepo(t)
	code, stdout, _ := lane.run("changelog", "--tag", "v1.6.6")
	if want := "Frozen words.\n"; code != 0 || stdout != want {
		t.Fatalf("changelog --tag v1.6.6 = (%d, %q), want (0, %q)", code, stdout, want)
	}
}

func TestChangelog_ATagThatIsNotAReleaseIsRefused(t *testing.T) {
	lane := historyRepo(t)
	for _, tag := range []string{"v9.9.9", "salvage/x", "1.7.0"} {
		code, stdout, stderr := lane.run("changelog", "--tag", tag)
		if code != 1 || stdout != "" || !strings.Contains(stderr, tag) {
			t.Errorf("changelog --tag %s = (%d, %q, %q), want exit 1 naming the tag", tag, code, stdout, stderr)
		}
	}
}

func TestChangelog_AFragmentItCannotReadIsNamedNotSkipped(t *testing.T) {
	lane := frozenRepo(t)
	lane.commit(map[string]string{"changelog.d/broken.md": "Words but no level.\n"})

	code, stdout, stderr := lane.run("changelog")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "changelog.d/broken.md") {
		t.Fatalf("changelog = (%d, %q, %q), want the broken fragment named", code, stdout, stderr)
	}
}

func TestChangelog_UsageErrorsAreTwo(t *testing.T) {
	for _, args := range [][]string{{"changelog", "extra"}, {"changelog", "--no-such-flag"}} {
		if code, _, _ := runCLI(args, ""); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
	}
}
