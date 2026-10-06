package postedit

// WallRedGreen is the red→green rule at PreToolUse, as a wall `aphrollo gate allow`
// can waive for a session: the override a deny of the rule names.
const WallRedGreen = "red-green"

// RedGreenWaived reports whether session has waived the red→green deny.
func RedGreenWaived(session string) bool { return waivedForSession(session, WallRedGreen) }
