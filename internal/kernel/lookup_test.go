package kernel

import "testing"

func TestLookupRule_namesTheRowItsDefaultLevelAndSection(t *testing.T) {
	cases := []struct {
		id      string
		section string
		class   RuleClass
		level   Level
	}{
		{"commit-proof", "§4 pre-commit, §5 earned", RuleEarned, LevelEnforce},
		{"primary-write", "§4 PreToolUse, §5 walls", RuleWall, LevelEnforce},
		{"red-green", "§3 TDD machine, §5 earned (tdd = enforce)", RuleEarned, LevelWarn},
		{"mutation", "§5 earned (pinned), §6 Mutation", RulePinned, LevelOff},
		{"long-wait", "§5 guide", RuleGuide, LevelWarn},
	}
	for _, c := range cases {
		r, ok := LookupRule(c.id)
		if !ok || r.ID != c.id || r.Section != c.section || r.Class != c.class {
			t.Errorf("LookupRule(%q) = %q %q %q ok=%v, want section %q class %q", c.id, r.ID, r.Section, r.Class, ok, c.section, c.class)
		}
		if got := r.DefaultLevel(); got != c.level {
			t.Errorf("%s default level = %q, want %q", c.id, got, c.level)
		}
	}
}

func TestLookupRule_refusesAnIdTheTableDoesNotHold(t *testing.T) {
	for _, id := range []string{"", "discard-bash", "ratchet:module_size", "COMMIT-PROOF"} {
		if r, ok := LookupRule(id); ok {
			t.Errorf("LookupRule(%q) found %q: the table holds no such rule", id, r.ID)
		}
	}
}

func TestInHoldout_isTheHashArmForAnEarnedBlockAndNothingElse(t *testing.T) {
	earned, _ := LookupRule("commit-proof")
	wall, _ := LookupRule("primary-write")
	held := laneHeldFor(t, "commit-proof")
	live := laneHeldFor(t, "primary-write", "commit-proof")
	if !InHoldout(held, earned) {
		t.Errorf("lane %q is in commit-proof's arm by the reference hash: InHoldout said no", held)
	}
	if InHoldout(live, earned) {
		t.Errorf("lane %q is live for commit-proof by the reference hash: InHoldout said yes", live)
	}
	wallHeld := laneHeldFor(t, "primary-write")
	if InHoldout(wallHeld, wall) {
		t.Errorf("lane %q hashes into the arm for primary-write, but a wall is never shadowed", wallHeld)
	}
}
