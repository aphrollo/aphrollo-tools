package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `/aphrollo off` reaches the prompt hook only when the harness knows the
// command: a managed skill named aphrollo, written by install, idempotent like
// the tdd one.
func TestWriteAphrolloSkill_WritesThenIsIdempotentAndNamesTheSwitch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	changed, err := WriteAphrolloSkill(dir)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "skills", "aphrollo", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: aphrollo", "/aphrollo off|on|status", "aphrollo gate userpromptsubmit", tddSkillMarker} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the skill lacks %q:\n%s", want, body)
		}
	}
	if changed, err := WriteAphrolloSkill(dir); err != nil || changed {
		t.Errorf("second write: changed=%v err=%v, want nothing written", changed, err)
	}
}

func TestRemoveAphrolloSkill_RemovesOurFileAndLeavesAUsersOwn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteAphrolloSkill(dir); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveAphrolloSkill(dir); err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	path := filepath.Join(dir, "skills", "aphrollo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("my own skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveAphrolloSkill(dir); err != nil || removed {
		t.Errorf("a file without our marker was removed: removed=%v err=%v", removed, err)
	}
}

// Doctor checks it: a config dir without the skill makes `/aphrollo off` an
// unknown command, which the managed-files check says.
func TestDoctorManagedFiles_ChecksTheAphrolloSkillToo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, write := range []func(string) (bool, error){WriteTDDSkill, WriteSDDSkill} {
		if _, err := write(dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteAgents(dir); err != nil {
		t.Fatal(err)
	}

	got := doctorManagedFiles(DoctorInput{ConfigDir: dir})

	if got.OK || !strings.Contains(got.Detail, "aphrollo/SKILL.md (missing)") {
		t.Fatalf("doctor must flag the missing aphrollo skill, got %+v", got)
	}
	if _, err := WriteAphrolloSkill(dir); err != nil {
		t.Fatal(err)
	}
	if got := doctorManagedFiles(DoctorInput{ConfigDir: dir}); !got.OK {
		t.Fatalf("with every managed file written the check must pass, got %+v", got)
	}
}
