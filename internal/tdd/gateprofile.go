package tdd

import (
	"os"
	"path/filepath"
	"strings"
)

// A nextest profile the WORKSPACE declares for gate runs. The default profile
// is tuned for a developer's own machine, where a test has the box to itself;
// the gate runs while other sessions build, and CPU-bound tests measured at
// 40-50 s alone walked past nextest's 60 s default under that load. The
// answer was becoming a habit of per-test exemptions, which weakens the suite
// permanently to fix a scheduling problem. A `[profile.gate]` table fixes it
// in one place instead, and only the GATE runs use it: posttooluse keeps the
// default, because an edit-time run that quietly waited longer would hide the
// slow test rather than report it.
const gateNextestProfile = "gate"

// hasGateProfile reports whether ws's checked-in nextest config declares
// `[profile.gate]`. Line-matched rather than parsed: the shape is a TOML table
// header, and a header is a whole line.
func hasGateProfile(ws string) bool {
	data, err := os.ReadFile(filepath.Join(ws, ".config", "nextest.toml"))
	if err != nil {
		return false
	}
	header := "[profile." + gateNextestProfile + "]"
	for line := range strings.Lines(string(data)) {
		if strings.TrimSpace(line) == header {
			return true
		}
	}
	return false
}

// withGateProfile adds `--profile gate` to a nextest run when the workspace
// declares the profile. A plain `cargo test` is left alone — it has no
// profiles — and so is any command that is not a nextest run, so a workspace
// that adds the table never changes what a non-nextest project runs.
func withGateProfile(r Runner, ws string) Runner {
	if !isNextestRun(r.Args) || !hasGateProfile(ws) {
		return r
	}
	args := append([]string{}, r.Args[:2]...)
	args = append(args, "--profile", gateNextestProfile)
	r.Args = append(args, r.Args[2:]...)
	return r
}

// isNextestRun reports whether a cargo Runner's args start `nextest run`.
func isNextestRun(args []string) bool {
	return len(args) >= 2 && args[0] == "nextest" && args[1] == "run"
}
