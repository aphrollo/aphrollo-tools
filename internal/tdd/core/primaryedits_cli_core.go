package core

// WallPrimary is the primary-checkout merge-only rule — the one wall this
// release's allow/revoke family covers. A later lane adds WallDiscard, and
// every wall shares this same mechanism: its refusal and its doc read the
// same regardless of which wall it names.
const WallPrimary = "primary"

// WallSourceBash is the wall refusing a Bash/PowerShell write of a source or
// test file in a gate-managed repo: those writes skip the per-edit gate the
// Edit/Write hooks run. `gate allow source-bash` waives it for the session.
const WallSourceBash = "source-bash"
