package cli

import (
	"github.com/aphrollo/aphrollo-tools/internal/store"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// horizonNote says where the repo's retained event log begins, "" when no month
// was ever swept. Retention removes event months past 16 weeks, so a report
// over "the whole log" covers only what is left, and says so rather than
// reading as if nothing older ever happened.
func horizonNote(repo string) string {
	at := store.SweptThrough(core.EventLogDir(repo))
	if at.IsZero() {
		return ""
	}
	return "events since " + at.UTC().Format("2006-01") + " (older months were swept by retention and are not counted)"
}
