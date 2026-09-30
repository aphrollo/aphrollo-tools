package install

import (
	"strings"
	"testing"
)

// A Bash call that writes source files is an edit like any other, and every
// place that teaches the gate says so, with the one difference: the write has
// happened before a deny law can judge it.
func TestBashWrites_AreTaughtAsEdits(t *testing.T) {
	t.Parallel()
	builder, ok := ManagedAgent("builder")
	if !ok {
		t.Fatal("the builder agent is not shipped")
	}
	texts := map[string]string{
		"the tdd skill":         TDDSkill(),
		"the builder template":  builder,
		"the managed CLAUDE.md": ClaudeMDBlock(BlockFlags{}),
	}
	for name, text := range texts {
		flat := strings.Join(strings.Fields(strings.ToLower(text)), " ")
		for _, want := range []string{
			"a bash script is fine for multi-file edits",
			"a deny law cannot refuse a bash write before it happens",
			"refused at commit",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s does not say %q", name, want)
			}
		}
	}
}
