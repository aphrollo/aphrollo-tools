package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateDoctor reports one line per install check and exits 1 if any FAILED.
// It changes nothing: every check names its own fix, and the fix is almost
// always `aphrollo install`.
func runGateDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		configDir = fs.String("config-dir", "", "Claude config dir (default: $CLAUDE_CONFIG_DIR or ~/.claude)")
		shimDir   = fs.String("shim-dir", "", "queue shim dir (default: <bindir>/cargo-queue)")
		repo      = fs.String("repo", ".", "repo whose CI configuration to judge (default: the working directory)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	out, code := tdd.RenderDoctor(tdd.Doctor(doctorInput(*configDir, *shimDir, *repo)))
	fmt.Fprint(stdout, out)
	return code
}

// doctorInput builds the tdd.DoctorInput the checks read, resolving each
// empty override to the same default runGateDoctor always used — shared with
// `check`'s doctor guard (runDoctorCheck below) so the two can never drift
// apart into judging a different install.
func doctorInput(configDir, shimDir, repo string) tdd.DoctorInput {
	bin := defaultBinPath()
	dir := configDir
	if dir == "" {
		dir = defaultClaudeDir()
	}
	shim := shimDir
	if shim == "" {
		shim = filepath.Join(filepath.Dir(bin), "cargo-queue")
	}
	return tdd.DoctorInput{
		ConfigDir:    dir,
		Bin:          bin,
		ShimDir:      shim,
		Repo:         repo,
		PathDirs:     userPathDirsFn(),
		GitHooksPath: gitHooksPathFn(),
	}
}

// userPathDirsFn indirects userPathDirs so a test can fake the PATH doctor
// judges without touching the box's own registry — mirroring the
// ratchetCheckFn seam other guards already use.
var userPathDirsFn = userPathDirs

// gitHooksPathFn indirects tdd.GlobalHooksPath so a test can fake the
// configured core.hooksPath doctor judges without reading the box's own
// global git config.
var gitHooksPathFn = tdd.GlobalHooksPath

// runDoctorCheck runs the doctor checks against repo with default resolution
// (no CLI overrides), writes tdd.RenderDoctor's report to w, and returns the
// number of FAILED checks — the miss count `check`'s doctor guard reports.
func runDoctorCheck(w io.Writer, repo string) int {
	checks := tdd.Doctor(doctorInput("", "", repo))
	out, _ := tdd.RenderDoctor(checks)
	fmt.Fprint(w, out)
	misses := 0
	for _, c := range checks {
		if !c.OK {
			misses++
		}
	}
	return misses
}

// machineEnvKey and userEnvKey are the two registry hives Windows composes a
// fresh process's PATH from. CreateProcess (via userenv's environment-block
// construction) concatenates the machine-wide value ahead of the per-user
// one, so a machine-wide git or cargo resolves before anything the user's own
// PATH names, even when the user put the shim dir first in THEIR list.
const (
	machineEnvKey = `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	userEnvKey    = `HKCU\Environment`
)

// userPathDirs is the PATH a freshly spawned process on this box actually
// resolves an unqualified command against — not as this process inherited
// it (a shell profile or a parent can prepend anything, so a check on
// os.Getenv("PATH") would pass on a box whose next session starts without
// the shim dir), and not just the user's OWN configuration either: a
// machine-wide install shadows the shim even when HKCU\Environment alone
// looks fine. On Windows that means reading both hives and combining them in
// the order Windows actually applies; elsewhere the environment is the
// configuration.
func userPathDirs() []string {
	if runtime.GOOS != "windows" {
		return filepath.SplitList(os.Getenv("PATH"))
	}
	machine := regPathDirs(machineEnvKey)
	user := regPathDirs(userEnvKey)
	if machine == nil && user == nil {
		return filepath.SplitList(os.Getenv("PATH"))
	}
	return combinePathScopes(machine, user)
}

// regPathDirs queries one registry hive's PATH value, returning nil when the
// query fails so the caller can fall back rather than judging a half-read
// scope as empty.
func regPathDirs(key string) []string {
	out, err := exec.Command("reg", "query", key, "/v", "Path").Output()
	if err != nil {
		return nil
	}
	return parseRegPath(string(out))
}

// combinePathScopes orders machine-wide entries ahead of the user's own,
// matching how Windows composes a fresh process's PATH from the two hives
// (see userPathDirs) — a check against the merged list sees what the shim
// actually shadows, not just what one scope contains.
func combinePathScopes(machine, user []string) []string {
	out := make([]string, 0, len(machine)+len(user))
	out = append(out, machine...)
	out = append(out, user...)
	return out
}

// parseRegPath pulls the value out of `reg query` output, whose data line is
// "    Path    REG_EXPAND_SZ    C:\a;C:\b". Expansion is applied because the
// user's PATH routinely stores %USERPROFILE% unexpanded.
func parseRegPath(out string) []string {
	for line := range strings.Lines(out) {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || !strings.EqualFold(fields[0], "Path") {
			continue
		}
		value := strings.TrimSpace(strings.SplitN(strings.TrimSpace(line), fields[1], 2)[1])
		// Semicolons, not filepath.SplitList: the registry value is a Windows
		// PATH whatever OS parses it, and on Linux the list separator is `:`,
		// which would cut every entry at its drive letter.
		return strings.Split(expandWindowsVars(value), ";")
	}
	return nil
}

// expandWindowsVars replaces %NAME% with the environment's value. os.ExpandEnv
// reads $NAME and would leave a stray delimiter behind if the percent signs
// were simply swapped for dollars. An unset name expands to nothing, which is
// what cmd.exe does with a bare `set` variable too.
func expandWindowsVars(s string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '%')
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		rest := s[open+1:]
		end := strings.IndexByte(rest, '%')
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:open])
		b.WriteString(os.Getenv(rest[:end]))
		s = rest[end+1:]
	}
}
