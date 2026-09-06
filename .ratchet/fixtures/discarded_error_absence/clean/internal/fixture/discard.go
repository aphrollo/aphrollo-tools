package fixture

// lookup is an honest not-found lookup: the return sits nowhere near an
// `if err != nil {` check, so a trigger alone must never be a hit.
func lookup(key string) (*int, bool) {
	v, ok := cache[key]
	if !ok {
		return nil, false
	}
	return v, true
}

// lookupChecked is the genuine case: the miss really is indistinguishable
// from an error by construction, and the escape says so on the line.
func lookupChecked(path string) (*int, bool) {
	data, err := readFile(path)
	if err != nil {
		return nil, false // absence-ok: a missing cache file is the honest "nothing recorded yet"
	}
	return parse(data), true
}
