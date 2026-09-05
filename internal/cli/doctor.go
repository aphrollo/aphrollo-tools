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
// always `aphrollo gate init`.
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
		ConfigDir: dir,
		Bin:       bin,
		ShimDir:   shim,
		Repo:      repo,
		PathDirs:  userPathDirsFn(),
	}
}

// userPathDirsFn indirects userPathDirs so a test can fake the PATH doctor
// judges without touching the box's own registry — mirroring the
// ratchetCheckFn seam other guards already use.
var userPathDirsFn = userPathDirs

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

// userPathDirs is the PATH as the USER has it configured, not as this process
// inherited it: a shell profile or a parent process can prepend anything, so a
// check that read os.Getenv("PATH") would pass on a box whose next session
// starts without the shim dir. On Windows that is HKCU\Environment; elsewhere
// the environment is the configuration.
func userPathDirs() []string {
	if runtime.GOOS != "windows" {
		return filepath.SplitList(os.Getenv("PATH"))
	}
	out, err := exec.Command("reg", "query", `HKCU\Environment`, "/v", "Path").Output()
	if err != nil {
		return filepath.SplitList(os.Getenv("PATH"))
	}
	return parseRegPath(string(out))
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
