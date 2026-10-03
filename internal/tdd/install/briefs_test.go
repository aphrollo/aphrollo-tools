package install

import "testing"

func TestBriefs_ListTheBlockTheSkillAndEveryAgentWithTheBytesInstalled(t *testing.T) {
	root := t.TempDir()
	skill := TDDSkill()
	want := []Brief{
		{Name: "managed CLAUDE.md block", Text: managedBlockFor(root)},
		{Name: "tdd skill", Text: skill},
	}
	for _, name := range managedAgentNames {
		text, ok := ManagedAgent(name)
		if !ok {
			t.Fatalf("no embedded agent %q", name)
		}
		want = append(want, Brief{Name: "agent " + name, Subagent: true, Text: text})
	}

	got := Briefs(root)
	if len(got) != len(want) {
		t.Fatalf("Briefs = %d items, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Briefs[%d] = {%q subagent=%v %d bytes}, want {%q subagent=%v %d bytes}",
				i, got[i].Name, got[i].Subagent, len(got[i].Text), want[i].Name, want[i].Subagent, len(want[i].Text))
		}
	}
}
