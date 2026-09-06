package pkg

// isUnset compares against the empty string literal, not a second path: no
// samePath remedy applies (#483), so this must stay clean.
func isUnset(path string) bool {
	return path == ""
}

// isAlsoUnset is the same shape from the other side.
func isAlsoUnset(path string) bool {
	return "" == path
}
