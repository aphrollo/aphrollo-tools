package cli

import (
	"flag"
	"fmt"
	"io"
)

const installUsage = `usage: aphrollo install [--repo <dir>] [--bin <path>] [--config-dir <dir>]
       [--git-hooks-dir <dir>] [--cargo-shim-dir <dir>] [--no-git]
       [--uninstall] [--claude-md] [--ratchet-readme]

Wires the whole gate in one command: session hooks + the global git gate,
CLAUDE.md/.ratchet/README.md/skills/agents (what "aphrollo gate init" did),
then --repo's own git-hook shims (what "aphrollo gate install --apply" did) —
in that order, so a single repo without the global gate is fully wired by one
call. --uninstall removes the session/git-gate side and stops there (the
repo's own shims have no separate uninstall). "gate init" and "gate install"
remain as aliases for one release.
`

// runInstall is the top-level `install` verb: gate init then gate install
// for --repo, reusing both bodies untouched — a merge of behavior, not a
// third implementation to keep in sync with the other two.
func runInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, installUsage)
		return 0
	}
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo         = fs.String("repo", ".", "repo whose hooks/CLAUDE.md/.ratchet/README.md this writes (default: the working directory's)")
		binPath      = fs.String("bin", "", "aphrollo binary the hooks invoke (default: this executable)")
		configDir    = fs.String("config-dir", "", "Claude config dir (default: $CLAUDE_CONFIG_DIR or ~/.claude)")
		gitHooksDir  = fs.String("git-hooks-dir", "", "git hooks dir for the global gate (default: $XDG_CONFIG_HOME/git/hooks or ~/.config/git/hooks)")
		cargoShimDir = fs.String("cargo-shim-dir", "", "dir for the cargo-queue shim (default: ~/.local/share/aphrollo/cargo-queue on Linux/macOS, alongside --bin on Windows)")
		noGit        = fs.Bool("no-git", false, "skip the git pre-commit gate; wire session hooks only")
		uninstall    = fs.Bool("uninstall", false, "remove the session hooks and git gate instead of installing them")
		claudeMD     = fs.Bool("claude-md", false, "write the managed CLAUDE.md block even when the repo has no CLAUDE.md yet")
		ratchetDoc   = fs.Bool("ratchet-readme", false, "write .ratchet/README.md even when the repo has no laws yet")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	initArgs := []string{"--repo", *repo}
	if *binPath != "" {
		initArgs = append(initArgs, "--bin", *binPath)
	}
	if *configDir != "" {
		initArgs = append(initArgs, "--config-dir", *configDir)
	}
	if *gitHooksDir != "" {
		initArgs = append(initArgs, "--git-hooks-dir", *gitHooksDir)
	}
	if *cargoShimDir != "" {
		initArgs = append(initArgs, "--cargo-shim-dir", *cargoShimDir)
	}
	if *noGit {
		initArgs = append(initArgs, "--no-git")
	}
	if *uninstall {
		initArgs = append(initArgs, "--uninstall")
	}
	if *claudeMD {
		initArgs = append(initArgs, "--claude-md")
	}
	if *ratchetDoc {
		initArgs = append(initArgs, "--ratchet-readme")
	}
	if code := runGateInit(initArgs, stdout, stderr); code != 0 {
		return code
	}
	if *uninstall {
		// gate install (the repo's own .git/hooks shims) has no uninstall of
		// its own; undoing the gate stops at the session/git-gate side above.
		return 0
	}
	installArgs := []string{"--repo", *repo, "--apply"}
	if *binPath != "" {
		installArgs = append(installArgs, "--bin", *binPath)
	}
	return runGateInstall(installArgs, stdout, stderr)
}
