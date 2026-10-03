// Package render owns every byte the agent reads (docs/trellis-architecture.md
// §2: "render owns every byte Claude reads"): the line grammar of §5, the
// hook-protocol envelopes of §4, and the rule for when a line counts as seen
// (§11 F22-F23, §12). It is pure: it imports the standard library and the
// kernel's types, reads no clock, no environment and no store, and returns
// bytes and data. Nothing here is wired to a hook; the adapters (F16 on) call it.
//
// # The grammar (§5, "The agent's experience")
//
// Each output kind is one function and yields a Line: Deny and Guide for a
// kernel.Decision, Green, Red, NotTested, Deferred and Stale for a Run, and
// Result, Decision and Effect to pick the right one. A line is plain text,
// deterministic, with no ANSI and no control characters: whatever text an
// adapter hands in is flattened to single spaces first (the raw run stays
// unfiltered in the store, §5 "trellis output"). The shapes are the doc's own:
//
//	trellis: green internal/lane (14 passed, 2.1s) · next: commit, or the next failing test
//	trellis: pending internal/lane TestOpenOnRed · run j42 queued · code edits stay open in internal/lane meanwhile
//	trellis: red internal/lane TestOpenOnRed · lane_test.go:41: want open, got closed · code edits open in internal/lane until it is green
//	trellis: red-bogus internal/store · build failed: store.go:88: undefined: lockPath · fix the setup; this is not a red
//	trellis: not tested internal/run · deps installing in lane fix-parse · last real: green @a1b2
//	trellis deny [primary-write] Write lands in the main checkout on trunk · do: EnterWorktree name=fix-parse · override: trellis allow primary-write --once · wrong? trellis feedback d-7f3a
//
// A deferred run is "pending", not "not tested" (§6 "Tiers"). A red carries
// its first failing assertion, up to 12 lines (§5 "Depth on demand"): the first
// rides the header, the rest follow indented, so a one-line assertion is the
// doc's one-line red. Guide and the Stale line have no sample in the doc and
// follow the deny's shape.
//
// # The caps (§5 "Tokens", §4 hook table)
//
// Tokens are bytes divided by four, rounded up. The caps are green 60, red
// 400, deny 120, guidance 60, brief 400, subagent brief 250. The doc names no
// cap for not-tested, pending and stale lines; they take the guidance cap,
// the smallest, so they stay one short line.
//
// A line never exceeds its cap, and never loses text silently. Rule id,
// override and the identifiers a line is about are kept whole up to a bound
// (a rule id is 48 bytes, an override 160); anything longer is cut, and the
// line ends in "· cut: <names>" naming every field that was cut. What can be
// squeezed (cause, detail, next, the assertion) is squeezed evenly, shortest
// first, so one long field cannot starve the others. A Line carries Cut with
// the same names, so a caller can count cuts. The golden files under testdata
// and a property test hold the caps; the kernel's own rule table is checked to
// need no cut at all.
//
// # The envelopes (§4, "Returns to Claude")
//
// PreToolUseDeny, PreToolUseAllow, Context and StopBlock return the JSON each
// hook needs, with the field names of the Claude Code hook contract. A deny is
// permissionDecision "deny" with permissionDecisionReason; guidance is
// additionalContext with no decision; nothing here ever emits updatedInput.
// PostToolBatch and SubagentStart additionalContext reach the agent (§12, F3
// results), the SubagentStart one only the subagent. Stop and SubagentStop
// block with a top-level decision and no hookSpecificOutput, which the harness
// drops. The text is clipped to its hook's cap the same way a line is.
//
// # Seen (§11 F22-F23, §12 "Context delivery", C3)
//
// A line counts as seen only after a delivery recorded as reaching the agent.
// Line.ID names the fact a line says (its kind, unit, test, tree and job, not
// its wording); Line.Deliver returns the Delivery record an engine appends once
// the hook output is written; Reaches says which hooks' output the agent
// reads (only PostToolBatch and SubagentStart, the two F3 recorded; see
// Reaches); Due says whether a line must be said again to an actor. A delivery to
// a hook that reaches nobody, or to another actor (a subagent's context is not
// the parent's), never counts. No store is read or written here.
package render
