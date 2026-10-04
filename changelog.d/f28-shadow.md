level: minor

The hooks now record, beside what they decide, what the new decision kernel would have decided for the same call, so a week of real use shows where the two agree before the kernel decides anything itself. Nothing a hook returns changes.

### What you will notice

- Each PreToolUse call that trips a rule the kernel also holds (a write into the primary checkout, discarding work, a direct PR open, attribution in an undercover repo, a law hit on an edit, a redundant whole-suite run) writes one `shadow` event to the event log. It names the kernel's rule, what the kernel would have done (block, warn, guide or allow), what the hook did, and how they compare: `agree`, `trellis-stricter` (the kernel would have blocked where the hook did not) or `trellis-softer` (the hook blocked where the kernel would only guide). A fire in the kernel's 10% holdout is marked `held_out`; no live rule changes level.
- Each finished run harvested at PostToolUse is recorded after the hook has answered, but only when it says something: a run the kernel reads differently from the gate (`verdict-mismatch`), a run the gate classed in a way the kernel's input cannot express, such as a bogus red (`not-comparable`, never counted as a mismatch), or one the kernel would guide on. A plain agreeing run writes nothing; `stats --shadow` derives those from the run results. Red-then-green at PreToolUse is not recorded: it needs per-unit state and edit coverage that no hook has yet.
- A record carries no command text. It is written after the hook has answered; the hook then waits up to 150 ms for it and drops it past that. That wait is left out of the hook's `hook.timing` seconds and reported apart as `shadow_ms`.
- `aphrollo stats --shadow` prints, per rule, the fires, the agreements, the would-be blocks and where the hook is stricter than the kernel. A would-be block is then counted as wrong (an override of the same rule on its lane within 10 minutes), a catch (a later commit gate refusal, red CI or escape on the lane), a pass (the lane merged) or still open, over a 28-day horizon and up to the lane's first merge. A fire in the primary checkout, on the trunk's name or on no lane stays open. The section says that the numbers come only from calls where a hook acted, so agreement is overstated, and that the kernel ran on its default config. A rule with fewer than 10 fires says so instead of printing a rate. A disagreement by itself is never counted as a wrong block.
- `aphrollo why <seq>` replays a shadow event with what followed it, and a deny now says how many shadow fires its rule has instead of "not recorded yet".

### What migrates by itself

- Nothing: the new events are ignored by every reader that does not know them.
