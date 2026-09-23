package tdd

import (
	"fmt"
	"strings"
)

// unattributedPrimaryLine is the stand-down for a harvest whose snapshot sat
// on a merge-only primary checkout and found changes no merge explains
// (issue #769). A write this hook can see into that checkout is refused
// before the command runs (bashPrimaryDecision), and a merge's own paths are
// handled above, so what is left is either another writer's work in the
// shared tree or a write the scanner could not see. Neither is a lane's
// edit: a suite run on it judges code in the primary and reports it as this
// command's verdict, a line naming the primary among a lane session's lines.
//
// A session that waived the wall (`aphrollo gate allow primary`) edits the
// primary on purpose, and a merge in progress there is being resolved by
// whoever holds it; both keep the ordinary run. "" whenever the question
// does not arise.
func unattributedPrimaryLine(session, root string, changed []string) string {
	if _, ok := PrimaryMergeOnly(root); !ok {
		return ""
	}
	if PrimaryEditsAllowed(session) || mergeInProgressRef(root) != "" {
		return ""
	}
	AppendGateLog("postedit", root, LogToken(changed[0]), "primary-unattributed-skipped", 0)
	return fmt.Sprintf("gate: → skipped in %s (%d changed path(s) in this merge-only primary checkout, which takes merges, not edits; "+
		"this command was not seen writing them, so they are no lane's edit: %s — if it did write them, it wrote the primary instead of its lane; the code was NOT tested)",
		root, len(changed), strings.Join(changed, ", "))
}
