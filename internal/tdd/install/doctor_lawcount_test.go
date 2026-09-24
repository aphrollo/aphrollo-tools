package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A repo whose laws dir is empty of declarations is not "0 laws, healthy" —
// it is the same as never having adopted the engine, so the check does not
// apply, matching the other opt-in checks (doctorCIClippyList,
// doctorLinterVersion).
func TestDoctor_SkipsTheLawCountCheckForARepoWithNoLaws(t *testing.T) {
	in := healthyInstall(t)
	if slices.Contains(checkNames(Doctor(in)), "ratchet laws") {
		t.Fatal("a repo with no .ratchet/laws must not report a law count")
	}
}

// The count is the whole point: a session (or a reviewer) asking "does this
// repo even have laws, and how many" gets an answer without opening
// .ratchet/laws and counting files by hand.
func TestDoctor_ReportsTheDeclaredLawCount(t *testing.T) {
	in := healthyInstall(t)
	lawsDir := filepath.Join(in.Repo, ".ratchet", "laws")
	if err := os.MkdirAll(lawsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLaw := func(name string) {
		body := "name = \"" + name + "\"\ndescription = \"d\"\nseverity = \"warn\"\n\n[scope]\ninclude = [\"**/*.go\"]\n\n[matcher]\nkind = \"line-count\"\nmax = 600\n"
		if err := os.WriteFile(filepath.Join(lawsDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLaw("a")
	writeLaw("b")

	c := check(t, Doctor(in), "ratchet laws")
	if !c.OK {
		t.Fatalf("a declared law set is healthy, not a failure: %+v", c)
	}
	if c.Detail != "2 law(s) declared" {
		t.Errorf("detail = %q, want the count", c.Detail)
	}
}

// A law that fails to parse is a real defect in the tree, not the same as
// having adopted no laws at all — it must report OK:false with the parse
// error, not silently skip the way an empty/absent laws dir does.
func TestDoctor_ReportsAParseFailureInsteadOfSkippingIt(t *testing.T) {
	in := healthyInstall(t)
	lawsDir := filepath.Join(in.Repo, ".ratchet", "laws")
	if err := os.MkdirAll(lawsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No severity: ParseLaw requires it, so this fails to parse.
	broken := "name = \"broken\"\ndescription = \"d\"\n"
	if err := os.WriteFile(filepath.Join(lawsDir, "broken.toml"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(checkNames(Doctor(in)), "ratchet laws") {
		t.Fatal("a law that fails to parse must still report, not silently skip")
	}
	c := check(t, Doctor(in), "ratchet laws")
	if c.OK {
		t.Fatalf("a law that fails to parse is not healthy: %+v", c)
	}
	if c.Detail == "" {
		t.Error("detail must carry the parse error")
	}
}
