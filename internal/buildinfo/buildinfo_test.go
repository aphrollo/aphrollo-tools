package buildinfo

import (
	"testing"
)

// A binary built with `go build -buildvcs=false` (the installer's build path)
// otherwise has no idea what it is. Stamp reports that honestly instead of
// printing a plausible-looking empty string.
func TestStamp_ReportsUnstampedWhenTheLinkerSetNothing(t *testing.T) {
	gotCommit, gotBuiltAt, gotStamped := Stamp()
	if wantCommit, wantBuiltAt, wantStamped := "", "", false; gotCommit != wantCommit || gotBuiltAt != wantBuiltAt || gotStamped != wantStamped {
		t.Fatalf("Stamp() = (%q, %q, %v), want (%q, %q, %v)", gotCommit, gotBuiltAt, gotStamped, wantCommit, wantBuiltAt, wantStamped)
	}
}

// A build at a release tag reports the tag's version, without the leading v a
// tag carries: that is the one string a repo's `requires` is compared with.
func TestVersion_ABuildAtAReleaseTagReportsThatTag(t *testing.T) {
	for _, stamp := range []string{"v1.7.0", "1.7.0"} {
		SetVersionForTest(stamp)
		t.Cleanup(func() { SetVersionForTest("") })
		if got, want := Version(), "1.7.0"; got != want {
			t.Errorf("stamp %q: Version() = %q, want %q", stamp, got, want)
		}
		if !Released() {
			t.Errorf("stamp %q: Released() = false, want true", stamp)
		}
	}
}

// A build that is not at a release tag has no version of its own. It says so,
// and names the commit it was built at when the linker stamped one.
func TestVersion_ADevBuildReportsDevAndItsCommit(t *testing.T) {
	SetForTest("ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2", "2026-09-05T02:57:00Z")
	t.Cleanup(func() { SetForTest("", "") })
	if got, want := Version(), "0.0.0-dev+ca47dba"; got != want {
		t.Fatalf("Version() = %q, want %q", got, want)
	}
	if Released() {
		t.Fatal("Released() = true for a build with no version stamp")
	}
}

func TestVersion_ABuildWithNoStampAtAllIsPlainDev(t *testing.T) {
	if got, want := Version(), "0.0.0-dev"; got != want {
		t.Fatalf("Version() = %q, want %q", got, want)
	}
	if Released() {
		t.Fatal("Released() = true for an unstamped build")
	}
}

// A stamp that is not MAJOR.MINOR.PATCH (a branch name, a pre-release, a
// truncated tag) must not become the version every `requires` is compared
// with: the build is a dev build.
func TestVersion_AStampThatIsNotASemanticVersionIsADevBuild(t *testing.T) {
	for _, stamp := range []string{"main", "1.7", "1.7.0-rc1", "v1.7.0\n", "01.7.0", "vv1.7.0"} {
		SetVersionForTest(stamp)
		t.Cleanup(func() { SetVersionForTest("") })
		if got, want := Version(), "0.0.0-dev"; got != want {
			t.Errorf("stamp %q: Version() = %q, want %q", stamp, got, want)
		}
		if Released() {
			t.Errorf("stamp %q: Released() = true, want false", stamp)
		}
	}
}

// ratchet: test_removed TestVersion_IsOneBareSemanticVersion: the version is no longer read from a VERSION file; a build reports its release tag or a dev version, which the tests above pin
