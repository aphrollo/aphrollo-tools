package tdd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRatchetHintFiresForAManifestWithNoLaws(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n")

	line := ratchetHintLine(root)
	if !strings.Contains(line, "no ratchet laws") || !strings.Contains(line, "Ratchet laws") {
		t.Fatalf("line = %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Errorf("the hint is ONE line, got %q", line)
	}
}

// An empty laws dir is the same as none: a directory nobody put a rule in
// enforces nothing, and reading its existence as an answer would silence the
// hint for the repo that most needs it.
func TestRatchetHintTreatsAnEmptyLawsDirAsNone(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n")
	if err := os.MkdirAll(filepath.Join(root, ".ratchet", "laws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ratchetHintLine(root) == "" {
		t.Fatal("an empty laws dir must still hint")
	}
}

func TestRatchetHintIsSilentOnceARepoHasALaw(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n[workspace.metadata.aphrollo]\nalways-run = [\"ratchet\"]\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "x.toml"), "name = \"x\"\n")

	if line := ratchetHintLine(root); line != "" {
		t.Fatalf("a repo with laws needs no hint, got %q", line)
	}
}

// The hint is about a project this gate can say something useful about; a
// directory that is not a repo, or a repo with no workspace manifest, gets
// nothing rather than advice about a layout it does not have.
func TestRatchetHintIsSilentWithoutAManifestOrARepo(t *testing.T) {
	bare := t.TempDir()
	gitInit(t, bare)
	if line := ratchetHintLine(bare); line != "" {
		t.Errorf("no manifest, no hint — got %q", line)
	}
	loose := t.TempDir()
	mustWrite(t, filepath.Join(loose, "Cargo.toml"), "[workspace]\n")
	if line := ratchetHintLine(loose); line != "" {
		t.Errorf("outside a repo, no hint — got %q", line)
	}
}

// SessionStart carries the hint alongside whatever else it says, and adds at
// most one line: a session start that scrolls is a session start nobody reads.
func TestSessionStartCarriesTheHintExactlyOnce(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// Session start also kicks off the detached disk sweep; observing it
	// instead of spawning keeps a real `gate gc` out of this test's temp dir.
	t.Cleanup(SetGCSpawnForTest(func(string) {}))

	msg := HandleSessionStart([]byte(`{"session_id":"s1","cwd":` + quoteJSON(root) + `}`))
	if strings.Count(msg, "no ratchet laws") != 1 {
		t.Fatalf("session start message = %q", msg)
	}
	if !strings.Contains(msg, skillNudge()) {
		t.Error("the hint must not displace what session start already says")
	}
}

// quoteJSON renders a path as a JSON string; on Windows it carries
// backslashes, which a hand-built payload has to escape or the hook sees a
// parse error instead of a cwd.
func quoteJSON(s string) string { return strconv.Quote(s) }
