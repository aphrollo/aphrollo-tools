package cli

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestManagedTemplates_NeverTeachARetiringAlias proves every skill and agent
// this tool writes verbatim into a user's config never carries a verb the
// binary's own --help marks as a retiring alias (#551): these files are read
// by a session (or an operator) exactly like CLAUDE.md's managed block, so a
// dead spelling in one of them names a command that no longer exists once
// the alias retires.
//
// Both sides come from the SAME source topLevelVerbTable/gateVerbTable that
// TestClaudeMDBlock_NeverTeachesARetiringAlias already reads from — nothing
// here hardcodes a verb name.
func TestManagedTemplates_NeverTeachARetiringAlias(t *testing.T) {
	bodies := map[string]string{
		"tdd skill": tdd.TDDSkill(),
		"sdd skill": tdd.SDDSkill(),
	}
	for _, name := range []string{"builder", "researcher", "reviewer"} {
		body, ok := tdd.ManagedAgent(name)
		if !ok {
			t.Fatalf("no embedded agent template named %q", name)
		}
		bodies["agent "+name] = body
	}
	for label, body := range bodies {
		t.Run(label, func(t *testing.T) {
			checkTable(t, body, topLevelVerbTable, "aphrollo ")
			checkTable(t, body, gateVerbTable, "gate ")
		})
	}
}
