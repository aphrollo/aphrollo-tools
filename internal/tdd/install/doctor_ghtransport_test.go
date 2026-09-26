package install

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeGHForDoctor builds a `gh` script in t.TempDir() that answers `api user`
// and `api graphql` independently, and prepends that dir to PATH for the
// test's duration. Cleaned up automatically: t.TempDir()/t.Setenv() both
// unwind at test end, no leftover process or file.
func fakeGHForDoctor(t *testing.T, restExit, graphqlExit int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  \"api user\") exit " + oneOrZero(restExit) + " ;;\n" +
		"  \"api graphql\") exit " + oneOrZero(graphqlExit) + " ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func oneOrZero(n int) string {
	if n == 0 {
		return "0"
	}
	return "1"
}

// TestDoctorGHTransport_MissingFails proves a box with no gh on PATH FAILS
// the check with a fix line naming install, not a silent "ok".
func TestDoctorGHTransport_MissingFails(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := doctorGHTransport()
	if c.OK {
		t.Fatalf("doctorGHTransport() = %+v, want FAIL for a missing gh", c)
	}
	if c.Detail == "" {
		t.Fatal("empty Detail for a missing gh")
	}
}

// TestDoctorGHTransport_UnauthenticatedFails proves a gh that resolves but
// cannot answer `api user` FAILS rather than reads as ready.
func TestDoctorGHTransport_UnauthenticatedFails(t *testing.T) {
	fakeGHForDoctor(t, 1, 1)
	c := doctorGHTransport()
	if c.OK {
		t.Fatalf("doctorGHTransport() = %+v, want FAIL for an unauthenticated gh", c)
	}
}

// TestDoctorGHTransport_RESTOnlyWarns proves REST-authenticated-but-blocked-
// GraphQL (the #880 cloud-container case) is OK with a WARN, not a FAIL —
// the workspace verbs route those calls over REST regardless.
func TestDoctorGHTransport_RESTOnlyWarns(t *testing.T) {
	fakeGHForDoctor(t, 0, 1)
	c := doctorGHTransport()
	if !c.OK {
		t.Fatalf("doctorGHTransport() = %+v, want OK (REST is enough)", c)
	}
	if !c.Warn {
		t.Fatal("Warn = false, want true — GraphQL is unavailable")
	}
}

// TestDoctorGHTransport_BothOK proves a fully working box reports OK with no
// warning.
func TestDoctorGHTransport_BothOK(t *testing.T) {
	fakeGHForDoctor(t, 0, 0)
	c := doctorGHTransport()
	if !c.OK || c.Warn {
		t.Fatalf("doctorGHTransport() = %+v, want OK and no warning", c)
	}
}
