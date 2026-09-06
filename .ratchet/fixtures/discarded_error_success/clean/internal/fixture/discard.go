package fixture

// lookupCached is an honest not-found lookup: the return sits nowhere near an
// `if err != nil {` check, so a trigger alone must never be a hit — that is
// the whole reason this matcher kind exists (regex-near is the complement of
// marker-within-lines, not a variant of it).
func lookupCached(raw string) (*int, error) {
	return nil, nil
}

// lookupIDChecked is the genuine case: the miss really is indistinguishable
// from an error by construction, and the escape says so on the line.
func lookupIDChecked(raw string) (*int, error) {
	v, err := parseInt(raw)
	if err != nil {
		return nil, nil // absence-ok: parseInt's own "not a number" error is indistinguishable from missing
	}
	return v, nil
}
