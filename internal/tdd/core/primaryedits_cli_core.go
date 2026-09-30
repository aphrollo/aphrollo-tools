package core

// WallPrimary is the primary-checkout merge-only rule — the one wall this
// release's allow/revoke family covers. A later lane adds WallDiscard, and
// every wall shares this same mechanism: its refusal and its doc read the
// same regardless of which wall it names.
const WallPrimary = "primary"
