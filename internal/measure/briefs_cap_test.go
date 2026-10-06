package measure

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The managed block, the tdd skill and each agent's brief are paid for in
// tokens by every session. The caps are the architecture's section 5; this is
// the check that stops a later edit growing a text back over its cap.
func TestBriefsCap_NoInstalledTextIsOverItsCap(t *testing.T) {
	t.Parallel()
	var in []Brief
	for _, b := range tdd.Briefs(t.TempDir()) {
		in = append(in, Brief{Name: b.Name, Subagent: b.Subagent, Bytes: len(b.Text)})
	}
	if len(in) != 5 {
		t.Fatalf("%d installed briefs, want the block, the skill and three agents", len(in))
	}
	for _, l := range CheckBriefs(in) {
		if l.Over {
			t.Errorf("%s is %d tokens (%d bytes), cap %d", l.Name, l.Tokens, l.Bytes, l.Cap)
		}
	}
}

// The block is rendered per repo, so its cap holds for a Go repo that pins
// undercover and measures commit mutants, the way this one does, and for a
// Cargo repo, not only for the bare block.
func TestBriefsCap_ARepoDeclaringItsFlagsStaysUnderTheBlockCap(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]tdd.BlockFlags{
		"go, undercover, commit mutants": {Go: true, Undercover: true, MutantsAtCommit: true},
		"cargo, undercover":              {Cargo: true, Undercover: true},
	} {
		l := CheckBriefs([]Brief{{Name: name, Bytes: len(tdd.ClaudeMDBlock(f))}})[0]
		if l.Over {
			t.Errorf("the managed block for %s is %d tokens (%d bytes), cap %d", name, l.Tokens, l.Bytes, l.Cap)
		}
	}
}
