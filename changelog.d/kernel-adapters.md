level: minor

The shadow record now covers red-green and stop-red: the kernel is asked what it would decide for each code edit and each block of Stop and SubagentStop, against the lane's real record, and the answer is recorded beside what the hooks did. Nothing a hook answers changes.

### What you will notice

- `aphrollo stats --shadow` has `red-green` and `stop-red` rows. A code edit is asked at PreToolUse where aphrollo always allows it (the proof is held at the commit), so every kernel guide or block counts as a would-be block; a refused commit proof on the lane, a passed proof, or `/tdd off` within ten minutes make it a catch, a pass or a wrong block.
- Both rules are asked under `tdd = enforce`, the level the architecture document blocks them at; the notes of the report say so.
- A finished run is folded into its lane's record (edit and run result per unit, a Go package or a project root), so the kernel reads the unit's phase as the runs left it. The unit is covered when a run of it was green on a tree holding its newest edit.
- A record that could not be made inside the 150 ms budget, or that lacks a lane, a unit or a tree key, is counted unjudged with its cause and never as agreement.
- Edits are folded into the lane's record when they are made, with no run, so a code edit asked about while a test's run is still on its way finds the unit pending and does not fire. Whether an edit adds a func or an exported symbol is read for Go only; every other language reads it as false.
- stop-red is asked at every Stop and SubagentStop, so an allow and a would-be block are recorded as well as a block; a red the lane record never folded is unjudged. Trunk lanes (main, master) and the primary checkout are not followed.
- A red-green pass only says the commit gate later proved red to green, which aphrollo already enforces, so passes are near-tautological and catches are rare by structure; `stats --shadow` prints how many records outran the budget.
