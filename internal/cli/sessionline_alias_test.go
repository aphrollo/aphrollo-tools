package cli

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestIssueSummaryLine_NeverTeachesARetiringAlias proves the session-start
// issue-summary line never tells an agent to run a verb the binary's own
// --help marks as a retiring alias (#551): the line prints at the start of
// every session in every repo the gate is installed in, so a dead spelling
// there names a command that no longer exists once the alias retires.
//
// internal/tdd cannot import internal/cli (cli already imports tdd), so this
// drives tdd's exported renderer directly rather than duplicating
// topLevelVerbTable/gateVerbTable on that side — the same tables
// TestClaudeMDBlock_NeverTeachesARetiringAlias already reads from.
func TestIssueSummaryLine_NeverTeachesARetiringAlias(t *testing.T) {
	line := tdd.RenderIssueSummary(3, map[string]int{"physics": 2, "netcode": 1}, nil, 1)
	checkTable(t, line, topLevelVerbTable, "aphrollo ")
	checkTable(t, line, gateVerbTable, "gate ")
}
