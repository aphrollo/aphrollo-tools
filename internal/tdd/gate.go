package tdd

// CmdName is the subcommand family the installed hooks, git shims and queue
// shims invoke. It was `tdd` while the gate only proved fail-first; the gate
// now also enforces the repo's declared laws, runs the quality stages and
// queues cargo and git, so the name says what it IS rather than the one thing
// it started as. `tdd` stays a silent alias on the CLI for one release, and
// every installer recognises BOTH spellings so a re-init replaces its own
// older entries instead of stacking a second hook beside them.
const CmdName = "gate"

// LegacyCmdName is the pre-rename spelling, recognised (never written) so an
// installer can find and replace what it wrote before.
const LegacyCmdName = "tdd"
