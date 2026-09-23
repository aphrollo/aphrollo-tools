package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillPath(dir string) string {
	return filepath.Join(dir, "skills", "tdd", "SKILL.md")
}

// The skill is a managed file like the CLAUDE.md block: written on init,
// byte-identical on a second run, so a re-init never churns the config dir.
func TestWriteTDDSkill_WritesThenIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	changed, err := WriteTDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first write reported no change")
	}
	body, err := os.ReadFile(skillPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	changed, err = WriteTDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second write reported a change over identical content")
	}
	again, err := os.ReadFile(skillPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(body) {
		t.Error("second write changed the file")
	}
}

// A hand edit is overwritten: the file says so in its header, and init is the
// only writer.
func TestWriteTDDSkill_OverwritesHandEdits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteTDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath(dir), []byte("hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := WriteTDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a hand-edited file must be rewritten")
	}
	if body, _ := os.ReadFile(skillPath(dir)); strings.Contains(string(body), "hand written") {
		t.Error("hand edit survived the rewrite")
	}
}

// Pins the CONTRACT the body must state, not its wording: the frontmatter the
// slash command and the skill loader read, the hook-interception notice (a
// model reading this after typing /tdd means the hook is missing), the gate's
// own classifications, the test-quality rules the gate cannot check, and the
// verification rule. Phrase checks, not a full-text compare, so prose edits
// are free and a dropped rule is not.
func TestTDDSkill_StatesItsContract(t *testing.T) {
	t.Parallel()
	body := TDDSkill()
	for _, want := range []string{
		"name: tdd",
		"argument-hint:",
		"aphrollo install",
		"aphrollo gate userpromptsubmit",
		"red-missing-impl",
		"red-bogus",
		"TIMEOUT",
		"mutation proof",
		"change detector",
		"DAMP",
		"cargo check",
		"go vet",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill body missing %q", want)
		}
	}
	if n := strings.Count(body, "\n"); n > 120 {
		t.Errorf("skill body is %d lines, want <= 120", n)
	}
	if !strings.HasPrefix(body, "---\n") {
		t.Error("skill must open with YAML frontmatter")
	}
}

// The skill replaces the old slash-command stub, which the hook intercepts by
// NAME: leaving both means two definitions of /tdd. Only the managed stub is
// removed -- a hand-written command of the same name is the user's.
func TestWriteTDDSkill_RetiresTheManagedCommandStub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := filepath.Join(dir, "commands", "tdd.md")
	if err := os.MkdirAll(filepath.Dir(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmd, []byte("intercepted by `aphrollo gate userpromptsubmit`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteTDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cmd); !os.IsNotExist(err) {
		t.Errorf("stale /tdd command stub survived: %v", err)
	}
}

func TestWriteTDDSkill_KeepsAForeignCommandStub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := filepath.Join(dir, "commands", "tdd.md")
	if err := os.MkdirAll(filepath.Dir(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmd, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteTDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cmd)
	if err != nil || string(body) != "my own notes\n" {
		t.Errorf("a command stub aphrollo did not write must be left alone: %q %v", body, err)
	}
}

func TestRemoveTDDSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteTDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveTDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("uninstall reported nothing removed")
	}
	if _, err := os.Stat(skillPath(dir)); !os.IsNotExist(err) {
		t.Errorf("skill file survived uninstall: %v", err)
	}
	if removed, err := RemoveTDDSkill(dir); err != nil || removed {
		t.Errorf("second uninstall = %v, %v; want false, nil", removed, err)
	}
}

// A skill of the same name this tool did not write is the user's file.
func TestRemoveTDDSkill_KeepsAForeignSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := skillPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: tdd\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveTDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Error("uninstall removed a skill aphrollo did not write")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("foreign skill was deleted: %v", err)
	}
}

// TestTDDSkill_TellsTheReaderHowToWaitOnADeferredVerdict pins the fix for a
// skill that said a deferred build's result "arrives at the next hook" but
// never how to wait for it: an agent that only reads that sentence ends its
// turn to wait for a notification, and the next hook is delivered BY its own
// next Edit/Write — idling on a deferred verdict deadlocks. Measured cost of
// this gap on 2026-09-21: ~246k tokens and 233 tool calls in that loop, 0
// commits landed until interrupted.
func TestTDDSkill_TellsTheReaderHowToWaitOnADeferredVerdict(t *testing.T) {
	t.Parallel()
	body := TDDSkill()
	for _, want := range []string{
		// The tree the BUILDING line names: a bare --wait resolves the
		// checkout from the shell cwd, which the harness resets away from
		// the lane that was edited (issue #732).
		"aphrollo gate status --wait <tree>",
		"aphrollo gate stats",
		"aphrollo gate output",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill body missing %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(body), "foreground") {
		t.Error("skill body must say to wait in the FOREGROUND, not end the turn")
	}
}

// TestManagedTemplates_CarryTheCurrentMarker proves tddSkillMarker (which
// RemoveTDDSkill, RemoveSDDSkill and RemoveAgents all match uninstall against)
// still appears verbatim in every template it is meant to identify. A
// template's own "Written by ..." line edited without updating the constant
// (or the reverse) breaks uninstall silently: a managed file stops being
// recognised as this tool's own, so removal skips it, or the marker drifts
// far enough that a foreign file could collide with it.
func TestManagedTemplates_CarryTheCurrentMarker(t *testing.T) {
	bodies := map[string]string{
		"tdd skill": TDDSkill(),
		"sdd skill": SDDSkill(),
	}
	for _, name := range managedAgentNames {
		body, ok := ManagedAgent(name)
		if !ok {
			t.Fatalf("no embedded agent template named %q", name)
		}
		bodies["agent "+name] = body
	}
	for label, body := range bodies {
		if !strings.Contains(body, tddSkillMarker) {
			t.Errorf("%s does not carry the marker %q — the template and tddSkillMarker have drifted", label, tddSkillMarker)
		}
	}
}

// Issue #753: a field mutation proof restored its file with `git checkout --`,
// the shim refused it (the file carried the lane's unstaged work too), and the
// only route the session found was the environment marker that destroys that
// work. "Restore byte-identically" named the outcome and not the tool, so the
// step has to name the route that restores the held working state instead of
// the index.
func TestTDDSkill_NamesTheRouteThatRestoresAMutationProof(t *testing.T) {
	t.Parallel()
	body := TDDSkill()
	for _, want := range []string{
		"aphrollo gate mutants hold <file>",
		"MUTATION=1 git checkout -- <file>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill body missing %q", want)
		}
	}
}
