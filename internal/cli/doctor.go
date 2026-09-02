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

	bin := defaultBinPath()
	dir := *configDir
	if dir == "" {
		dir = defaultClaudeDir()
	}
	shim := *shimDir
	if shim == "" {
		shim = filepath.Join(filepath.Dir(bin), "cargo-queue")
	}
	out, code := tdd.RenderDoctor(tdd.Doctor(tdd.DoctorInput{
		ConfigDir: dir,
		Bin:       bin,
		ShimDir:   shim,
		Repo:      *repo,
		PathDirs:  userPathDirs(),
	}))
	fmt.Fprint(stdout, out)
	return code
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
		return filepath.SplitList(expandWindowsVars(value))
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
