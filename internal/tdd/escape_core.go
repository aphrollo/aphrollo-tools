package tdd

// fitRunes shortens s to at most n runes, marking that it was cut.
func fitRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}
