package buildinfo

import "testing"

// A binary built with `go build -buildvcs=false` (the installer's build path)
// otherwise has no idea what it is. Stamp reports that honestly instead of
// printing a plausible-looking empty string.
func TestStamp_ReportsUnstampedWhenTheLinkerSetNothing(t *testing.T) {
	gotCommit, gotBuiltAt, gotStamped := Stamp()
	if wantCommit, wantBuiltAt, wantStamped := "", "", false; gotCommit != wantCommit || gotBuiltAt != wantBuiltAt || gotStamped != wantStamped {
		t.Fatalf("Stamp() = (%q, %q, %v), want (%q, %q, %v)", gotCommit, gotBuiltAt, gotStamped, wantCommit, wantBuiltAt, wantStamped)
	}
}
