package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The mutation hold is what makes a hand mutation proof restorable: the
// pre-mutation WORKING state of one file, kept outside the repo, so the
// restore puts back what the proof started from rather than what HEAD holds.
// A lane's file normally carries uncommitted work of its own mid-proof, and
// `git checkout --` would take it (issue #650).

func holdFixture(t *testing.T) (dir, file string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(sessionEnv, "s-hold-"+t.Name())
	dir = t.TempDir()
	file = filepath.Join(dir, "victim.go")
	if err := os.WriteFile(file, []byte("package p\n\nfunc F() int { return 7 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func TestHoldMutation_RestoresTheWorkingStateNotTheCommittedOne(t *testing.T) {
	_, file := holdFixture(t)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HoldMutation(file); err != nil {
		t.Fatalf("HoldMutation: %v", err)
	}
	if err := os.WriteFile(file, []byte("package p\n\nfunc F() int { return 8 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreMutationHold(file); err != nil {
		t.Fatalf("RestoreMutationHold: %v", err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("restored %q, want the held bytes %q", string(after), string(before))
	}
}

func TestMutationHoldFor_AnswersFalseForAFileNoProofHeld(t *testing.T) {
	dir, file := holdFixture(t)
	if _, ok := MutationHoldFor(file); ok {
		t.Fatal("MutationHoldFor on a file with no hold = true, want false")
	}
	if _, err := HoldMutation(file); err != nil {
		t.Fatal(err)
	}
	if _, ok := MutationHoldFor(file); !ok {
		t.Fatal("MutationHoldFor after HoldMutation = false, want true")
	}
	// A sibling in the same directory is a different file, not the held one.
	if _, ok := MutationHoldFor(filepath.Join(dir, "other.go")); ok {
		t.Fatal("MutationHoldFor on a sibling = true, want false")
	}
}

func TestHoldMutation_CoversAnUntrackedFile(t *testing.T) {
	dir, _ := holdFixture(t)
	// Untracked by construction: this directory is not a repo at all, and the
	// hold is a byte copy either way — which is the whole reason the restore
	// covers what `git checkout -- <path>` never could.
	newFile := filepath.Join(dir, "brand-new.go")
	if err := os.WriteFile(newFile, []byte("package p // v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HoldMutation(newFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(newFile); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreMutationHold(newFile); err != nil {
		t.Fatalf("RestoreMutationHold of a deleted untracked file: %v", err)
	}
	body, err := os.ReadFile(newFile)
	if err != nil {
		t.Fatalf("the untracked file was not recreated: %v", err)
	}
	if string(body) != "package p // v1\n" {
		t.Fatalf("restored %q, want the held bytes", string(body))
	}
}

func TestHoldMutation_RefusesAFileItCannotRead(t *testing.T) {
	dir, _ := holdFixture(t)
	if _, err := HoldMutation(filepath.Join(dir, "not-here.go")); err == nil {
		t.Fatal("HoldMutation on a missing file = nil error, want a refusal: there is no working state to hold")
	}
}

func TestMutationHoldFor_StopsHonouringAStaleHold(t *testing.T) {
	_, file := holdFixture(t)
	h, err := HoldMutation(file)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(h.At) > time.Minute {
		t.Fatalf("hold taken at %v, want now", h.At)
	}
	restore := SetMutationHoldClockForTest(func() time.Time { return h.At.Add(MutationHoldTTL + time.Minute) })
	defer restore()

	if _, ok := MutationHoldFor(file); ok {
		t.Fatal("MutationHoldFor past the TTL = true, want false: a proof does not last that long, and a stale hold would restore hours-old content")
	}
	if _, err := RestoreMutationHold(file); err == nil {
		t.Fatal("RestoreMutationHold past the TTL = nil error, want a refusal")
	}
}

func TestHoldMutation_IsScopedToTheSession(t *testing.T) {
	_, file := holdFixture(t)
	if _, err := HoldMutation(file); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sessionEnv, "s-hold-someone-else")
	if _, ok := MutationHoldFor(file); ok {
		t.Fatal("another session sees this session's hold, want false")
	}
}

func TestHoldMutation_WithoutASessionSaysSo(t *testing.T) {
	_, file := holdFixture(t)
	t.Setenv(sessionEnv, "")
	t.Setenv(sessionCodeEnv, "")
	_, err := HoldMutation(file)
	if err == nil {
		t.Fatal("HoldMutation with no session = nil error, want a refusal naming the missing session")
	}
	if !strings.Contains(err.Error(), "session") {
		t.Fatalf("error = %q, want it to name the missing session", err.Error())
	}
}
