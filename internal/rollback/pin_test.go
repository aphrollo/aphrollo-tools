package rollback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateDir gives the test its own gate state dir, so one test's pin is never
// another's.
func stateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return filepath.Join(dir, "gate-state")
}

const (
	commitA = "1111111111111111111111111111111111111111"
	commitB = "2222222222222222222222222222222222222222"
)

func TestPin_ReadsBackWhatWasWritten(t *testing.T) {
	stateDir(t)

	if err := WritePin(Pin{Ref: "v1.3.0", Commit: commitA, At: "2026-10-02T10:00:00Z"}); err != nil {
		t.Fatalf("WritePin: %v", err)
	}

	got, state := ReadPin()
	if state != Pinned {
		t.Fatalf("state = %v, want Pinned", state)
	}
	want := Pin{Schema: 1, Ref: "v1.3.0", Commit: commitA, At: "2026-10-02T10:00:00Z"}
	if got != want {
		t.Fatalf("ReadPin() = %+v, want %+v", got, want)
	}
}

func TestPin_NoFileIsNoPin(t *testing.T) {
	stateDir(t)

	if _, state := ReadPin(); state != Unpinned {
		t.Fatalf("state = %v, want Unpinned", state)
	}
}

// A pin with no commit names nothing to stay on, so honouring it would strand
// the box on an unknown binary.
func TestPin_WithoutACommitIsNoPin(t *testing.T) {
	dir := stateDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PinPath(), []byte(`{"schema":1,"ref":"v1.3.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, state := ReadPin(); state != Unpinned {
		t.Fatalf("state = %v, want Unpinned for a pin that names no commit", state)
	}
}

// A newer aphrollo owns the file's shape: this one must neither guess at it nor
// replace it.
func TestPin_NewerSchemaIsUnreadableAndNeverOverwrittenOrCleared(t *testing.T) {
	dir := stateDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	newer := `{"schema":2,"ref":"v9.0.0","commit":"` + commitB + `","extra":"x"}`
	if err := os.WriteFile(PinPath(), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, state := ReadPin(); state != PinUnreadable {
		t.Fatalf("state = %v, want PinUnreadable", state)
	}
	if err := WritePin(Pin{Ref: "v1.0.0", Commit: commitA}); err == nil {
		t.Fatal("WritePin over a newer pin file: want an error")
	}
	if _, err := ClearPin(); err == nil {
		t.Fatal("ClearPin of a newer pin file: want an error")
	}
	got, err := os.ReadFile(PinPath())
	if err != nil || string(got) != newer {
		t.Fatalf("the newer pin file changed: %q (%v)", got, err)
	}
}

// A file that does not parse is evidence of a torn write: it is moved aside
// rather than read as a pin or silently deleted.
func TestPin_CorruptFileIsMovedAsideAndReadsAsNoPin(t *testing.T) {
	dir := stateDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PinPath(), []byte(`{"schema":1,"ref":`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, state := ReadPin(); state != Unpinned {
		t.Fatalf("state = %v, want Unpinned", state)
	}
	if _, err := os.Stat(PinPath()); !os.IsNotExist(err) {
		t.Fatalf("the corrupt pin file is still in place: %v", err)
	}
	aside, _ := filepath.Glob(PinPath() + ".corrupt-*")
	if len(aside) != 1 {
		t.Fatalf("want the corrupt file kept as evidence, found %v", aside)
	}
}

func TestClearPin_RemovesThePinAndSaysWhetherThereWasOne(t *testing.T) {
	stateDir(t)
	if err := WritePin(Pin{Ref: "v1.3.0", Commit: commitA}); err != nil {
		t.Fatal(err)
	}

	cleared, err := ClearPin()
	if err != nil || !cleared {
		t.Fatalf("ClearPin() = (%v, %v), want (true, nil)", cleared, err)
	}
	if _, state := ReadPin(); state != Unpinned {
		t.Fatalf("state after clearing = %v, want Unpinned", state)
	}
	cleared, err = ClearPin()
	if err != nil || cleared {
		t.Fatalf("second ClearPin() = (%v, %v), want (false, nil)", cleared, err)
	}
}

func TestPinDescribe_NamesATagWithItsCommitAndACommitOnce(t *testing.T) {
	cases := []struct {
		name string
		pin  Pin
		want string
	}{
		{"tag", Pin{Ref: "v1.3.0", Commit: commitA}, "v1.3.0 (1111111)"},
		{"short sha", Pin{Ref: "1111111", Commit: commitA}, "1111111"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pin.Describe(); got != c.want {
				t.Fatalf("Describe() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPinNotice_SaysPinnedToWhatAndHowToLeaveIt(t *testing.T) {
	stateDir(t)
	if err := WritePin(Pin{Ref: "v1.3.0", Commit: commitA, At: "2026-10-02T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	line, ok := PinNotice()
	if !ok {
		t.Fatal("PinNotice: want a notice for a pinned box")
	}
	for _, want := range []string{"pinned to v1.3.0 (1111111)", "aphrollo update --unpin"} {
		if !strings.Contains(line, want) {
			t.Errorf("notice %q lacks %q", line, want)
		}
	}
}

func TestPinNotice_SilentWhenNotPinned(t *testing.T) {
	stateDir(t)

	if line, ok := PinNotice(); ok || line != "" {
		t.Fatalf("PinNotice() = (%q, %v), want silence", line, ok)
	}
}

func TestPinNotice_NamesAPinItCannotRead(t *testing.T) {
	dir := stateDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PinPath(), []byte(`{"schema":2,"commit":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	line, ok := PinNotice()
	if !ok || !strings.Contains(line, "newer aphrollo") {
		t.Fatalf("PinNotice() = (%q, %v), want a notice naming the newer aphrollo", line, ok)
	}
}
