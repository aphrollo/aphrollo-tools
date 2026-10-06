package cli

import (
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runPostToolUse is the PostToolUse hook: the answer to an edit or a shell call.
func runPostToolUse(raw []byte, stdout io.Writer) int {
	// PostToolUse never blocks: it only ever emits advisory context. It is
	// also the one hook allowed to leave work running past its budget — a
	// cold Bevy build does not fit in 110s and killing it establishes
	// nothing. A retro a merge in this session left pending rides after
	// the hook's own text, once.
	tdd.EnableDeferredPhases(true)
	// A suite the agent ran itself is a run: folded after the answer (shadow.Flush).
	tdd.FoldBashRun(raw)
	if tdd.IsBashHook(raw) {
		payload, code := tdd.RenderPostToolUse(tdd.WithPendingRetro(raw, tdd.PostBash(raw, tdd.RunSuite(postEditBudget()))))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	}
	payload, code := tdd.RenderPostToolUse(tdd.WithPendingRetro(raw, tdd.PostEdit(raw, tdd.RunSuite(postEditBudget()))))
	if len(payload) > 0 {
		stdout.Write(payload)
	}
	return code
}
