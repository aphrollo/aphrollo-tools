package fixture

// readMutantsJob mirrors the real defect (issue #310): a read failure and a
// JSON-parse error both fold into a bare false, which the caller turns into a
// silent success — see .ratchet/laws/discarded_error_absence.toml.
func readMutantsJob(path string) (*int, bool) {
	data, err := readFile(path)
	if err != nil {
		return nil, false
	}
	v := parse(data)
	return v, true
}
