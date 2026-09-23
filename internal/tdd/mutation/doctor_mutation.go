package mutation

// DoctorCheck is one check's verdict. Warn marks a finding the report shows
// but does not fail on: a fact about the repo rather than a broken install.
type DoctorCheck struct {
	Name   string
	OK     bool
	Warn   bool
	Detail string
}

// DoctorInput is everything the checks read that is not a file: the config dir
// they judge, the binary they judge against, and the PATH they were resolved
// from. Injected rather than looked up, so a test drives every branch without
// touching the box's own registry or environment.
type DoctorInput struct {
	ConfigDir string
	Bin       string
	ShimDir   string
	Repo      string
	PathDirs  []string
	// GitHooksPath is the box's current global core.hooksPath, resolved by
	// the caller (empty when unset) — injected the same way PathDirs is, so
	// a test drives every branch without reading or writing the box's own
	// git config.
	GitHooksPath string
}
