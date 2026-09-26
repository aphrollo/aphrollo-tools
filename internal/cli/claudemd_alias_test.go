package cli

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestClaudeMDBlock_NeverTeachesARetiringAlias proves the managed block
// `aphrollo gate init` writes into every repo never instructs a session to
// run a verb the binary's own --help marks as a retiring alias (#518): the
// block is the operating manual an agent reads at the start of every
// session in every repo the gate is installed in, so a dead spelling there
// does not surface as a deprecation warning when the alias retires — it
// surfaces as a command that no longer exists, in every repo at once.
//
// Both sides come from the SAME source topLevelVerbTable/gateVerbTable
// already are — the tables surface_test.go's TestSurface_TablesMatchDispatch
// proves agree with the real dispatch, and TestSurface_TablesMarkAliasesAsSuch
// proves carry Alias/Of correctly. Nothing here hardcodes a verb name: a
// fourth alias added to either table is caught the moment the block
// mentions it, with no edit to this test required.
func TestClaudeMDBlock_NeverTeachesARetiringAlias(t *testing.T) {
	for _, undercover := range []bool{false, true} {
		block := tdd.ClaudeMDBlock(tdd.BlockFlags{Undercover: undercover})
		checkTable(t, block, topLevelVerbTable, "aphrollo ")
		checkTable(t, block, gateVerbTable, "gate ")
	}
}

// checkTable fails t for every alias in table whose phrase (prefix + the
// alias's own name, e.g. "aphrollo init" or "gate issue") appears in block —
// shared by every generated-text surface that must never teach a retiring
// alias (#551), so each surface's own test names only the block it renders
// and the two tables, never a hardcoded verb.
func checkTable(t *testing.T, block string, table []Verb, prefix string) {
	t.Helper()
	for _, v := range table {
		if !v.Alias {
			continue
		}
		phrase := prefix + v.Name
		if strings.Contains(block, phrase) {
			t.Errorf("output contains %q, a retiring alias of %q — point it at the canonical spelling instead", phrase, v.Of)
		}
	}
}
