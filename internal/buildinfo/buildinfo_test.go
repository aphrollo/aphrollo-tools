package buildinfo

import (
	"regexp"
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

// Every build carries the semantic version next to the stamp, including one
// built by hand with no linker flags. A version with a trailing newline from
// the file it is read out of would print as two lines and never compare equal
// to anything a repo's `requires` names.
func TestVersion_IsOneBareSemanticVersion(t *testing.T) {
	got := Version()
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(got) {
		t.Fatalf("Version() = %q, want MAJOR.MINOR.PATCH with no surrounding whitespace", got)
	}
}
