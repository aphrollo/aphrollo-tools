package mutation

// DoctorCheck is one check's verdict. Warn marks a finding the report shows
// but does not fail on: a fact about the repo rather than a broken install.
type DoctorCheck struct {
	Name string
	OK   bool
	Warn bool
	// Info marks an ok finding that is a fact to read, not a state to fix:
	// it prints as "info" and never counts as a miss.
	Info   bool
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
	// ProcessPathDirs is the PATH this process itself has: what a shell the
	// agent harness starts resolves `git`/`cargo` against. Empty means the
	// caller did not read it, and the shim check falls back to PathDirs.
	ProcessPathDirs []string
	// GitHooksPath is the box's current global core.hooksPath, resolved by
	// the caller (empty when unset) — injected the same way PathDirs is, so
	// a test drives every branch without reading or writing the box's own
	// git config.
	GitHooksPath string
	// ShimBypassLine is the caller's own exec.LookPath-based finding of
	// whether `git`/`cargo` on THIS PROCESS's PATH resolve outside ShimDir —
	// "" when they don't, or when the shims were never installed there at
	// all. Injected rather than looked up here for the same reason as
	// PathDirs: exec.LookPath reads this box's real PATH, and a check that
	// called it directly could never be driven from a test.
	ShimBypassLine string
	// UserPathDirs is the user-scope (HKCU) PATH entries, expanded, as the
	// caller read them from the registry; nil off Windows or when the read
	// failed, which makes the user-PATH check not applicable.
	UserPathDirs []string
	// LauncherDir is the dir the user PATH should carry for `aphrollo`: the
	// user-space root for a user-space binary, else Bin's own dir ("" means
	// Bin's dir).
	LauncherDir string
}
