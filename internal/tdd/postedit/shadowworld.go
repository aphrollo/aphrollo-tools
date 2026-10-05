package postedit

import (
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// shadowWorld is what the red-green and stop-red shadow records read of the box
// (internal/shadow World), from the same places the live hooks read it: the branch
// from the repository's files, the project root from the edit hook's marker walk,
// the edit ledger, and the lane store the run wrapper writes its verdicts to. None
// of it spawns git.
func shadowWorld() shadow.World {
	return shadow.World{
		Lane:        LaneOf,
		ProjectRoot: FindProjectRoot,
		Edits:       shadowLedger,
		Open: func(root string) (*store.Store, error) {
			return store.Open(EventLogDir(root), store.Options{Config: shadow.Config, Warn: func(string) {}})
		},
	}
}

// ShadowWorld is shadowWorld for the hooks outside this package that record shadow
// events (the PreToolUse and Stop hooks).
func ShadowWorld() shadow.World { return shadowWorld() }

// shadowLedger is the edit ledger of a project root as the shadow records read it.
func shadowLedger(projectRoot string) []shadow.LedgerEdit {
	var out []shadow.LedgerEdit
	for _, e := range loadEditLedger(projectRoot) {
		out = append(out, shadow.LedgerEdit{ID: e.ID, File: e.File, At: e.At})
	}
	return out
}
