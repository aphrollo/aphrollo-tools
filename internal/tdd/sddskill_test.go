package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sddSkillFile(dir string) string {
	return filepath.Join(dir, "skills", "sdd", "SKILL.md")
}

// TestWriteSDDSkill_WritesThenIsIdempotent pins the same managed-file contract
// the tdd skill carries: written on init, byte-identical on a second run, so a
// re-init never churns the config dir.
func TestWriteSDDSkill_WritesThenIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	changed, err := WriteSDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first write reported no change")
	}
	body, err := os.ReadFile(sddSkillFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	changed, err = WriteSDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second write reported a change over identical content")
	}
	again, err := os.ReadFile(sddSkillFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(body) {
		t.Error("second write changed the bytes")
	}
}

// TestSDDSkill_StatesItsContract names what the skill has to say to be worth
// installing: the four phases, the two artefacts, where the spec dir comes
// from, and that the tree is deleted at the end. A skill missing any of those
// sends a session to invent its own process.
func TestSDDSkill_StatesItsContract(t *testing.T) {
	t.Parallel()
	body := SDDSkill()
	for _, want := range []string{
		"/sdd <slug>",
		"spec.md",
		"plan.md",
		"one at a time",
		"acceptance criteria",
		"lane",
		"`tdd` skill",
		"sdd-dir",
		"docs/sdd",
		"decisions.md",
		"DELETE",
	} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("skill body missing %q", want)
		}
	}
	if n := strings.Count(body, "\n"); n > 80 {
		t.Errorf("skill body is %d lines, want <= 80", n)
	}
	if !strings.HasPrefix(body, "---\n") {
		t.Error("skill must open with YAML frontmatter")
	}
	if strings.Contains(body, "\r") {
		t.Error("skill carries a carriage return")
	}
	// The retired plugin the content came from must not be named anywhere:
	// the procedure ships with this binary and depends on no plugin install.
	for _, banned := range []string{"superpowers", "brainstorming", "writing-plans", "executing-plans"} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("skill names the retired plugin surface %q", banned)
		}
	}
}

// TestRemoveSDDSkill_LeavesASkillThisToolNeverWrote is the ownership limit:
// a hand-written skill of the same name carries no marker and is the user's.
func TestRemoveSDDSkill_LeavesASkillThisToolNeverWrote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteSDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveSDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("the managed skill must be removed")
	}

	mine := sddSkillFile(dir)
	if err := os.WriteFile(mine, []byte("---\nname: sdd\n---\n\nmy own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err = RemoveSDDSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("a skill without the marker is the user's and must survive")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("the user's own skill was deleted: %v", err)
	}
}
