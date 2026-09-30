package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const installUsage = `usage: aphrollo install [--repo <dir>] [--bin <path>] [--config-dir <dir>]
       [--git-hooks-dir <dir>] [--cargo-shim-dir <dir>] [--no-git]
       [--uninstall] [--claude-md] [--ratchet-readme]
       aphrollo install --managed-block-only [--repo <dir>] [--claude-md]

Wires the whole gate in one command: session hooks + the global git gate,
CLAUDE.md/.ratchet/README.md/skills/agents (what "aphrollo gate init" did),
then --repo's own git-hook shims (what "aphrollo gate install" did) —
in that order, so a single repo without the global gate is fully wired by one
call. --uninstall removes the session/git-gate side and stops there (the
repo's own shims have no separate uninstall). "gate init" and "gate install"
remain as aliases for one release.

--no-git writes no git hook anywhere: not the global gate, not the queue
shims, and not --repo's own shims (a linked worktree shares the primary's
.git/hooks). A hook or shim never points at a binary in a temp, go-build or
.worktrees location unless --bin names it.

--managed-block-only re-renders --repo's CLAUDE.md managed block and writes
nothing else: use it from a lane to refresh the committed block instead of a
full install.
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
		noGit        = fs.Bool("no-git", false, "skip the global git gate and the queue shims; still wire session hooks, skills, agents and the repo's CLAUDE.md block")
		uninstall    = fs.Bool("uninstall", false, "remove the session hooks and git gate instead of installing them")
		claudeMD     = fs.Bool("claude-md", false, "write the managed CLAUDE.md block even when the repo has no CLAUDE.md yet")
		ratchetDoc   = fs.Bool("ratchet-readme", false, "write .ratchet/README.md even when the repo has no laws yet")
		blockOnly    = fs.Bool("managed-block-only", false, "only re-render the repo's CLAUDE.md managed block; write no hook, shim, skill, agent or config")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *blockOnly {
		return writeManagedBlock(*repo, *claudeMD, stdout, stderr)
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
	if *noGit {
		// The repo's own shims land in the git dir every linked worktree
		// shares, so they are git hooks like the global gate's.
		return printFeatures(*repo, stdout, stderr)
	}
	installArgs := []string{"--repo", *repo}
	if *binPath != "" {
		installArgs = append(installArgs, "--bin", *binPath)
	}
	if code := runGateInstall(installArgs, stdout, stderr); code != 0 {
		return code
	}
	return printFeatures(*repo, stdout, stderr)
}

// printFeatures prints the opt-in table, once per repo: install stays
// non-interactive and names the key to set rather than setting it. Failing to
// record what was shown costs a warning, never the install that already
// succeeded.
func printFeatures(repo string, stdout, stderr io.Writer) int {
	if root := tdd.RepoRoot(repo); root != "" {
		text, err := tdd.FeaturesNotYetShown(root)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo install: %v\n", err)
			return 0
		}
		fmt.Fprint(stdout, text)
	}
	return 0
}
