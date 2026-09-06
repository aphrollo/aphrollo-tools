package fixture

// lookupID mirrors ghViewPRReal's pre-#348 shape: ANY error from parseInt is
// read as "nothing to report" rather than propagated, which is the defect
// discarded_error_success exists to catch — see .ratchet/laws/discarded_error_success.toml.
func lookupID(raw string) (*int, error) {
	v, err := parseInt(raw)
	if err != nil {
		return nil, nil
	}
	return v, nil
}
