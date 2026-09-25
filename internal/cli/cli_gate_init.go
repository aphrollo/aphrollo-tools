package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateInit wires (or, with --uninstall, removes) the aphrollo tdd session
// hooks in a Claude config dir's settings.json. It is the native replacement
// for the retired claude-code-tdd install.sh: idempotent, backs up any existing
// file, and resolves the config dir + invoked binary from sensible defaults.
func runGateInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		configDir    = fs.String("config-dir", "", "Claude config dir (default: $CLAUDE_CONFIG_DIR or ~/.claude)")
		binPath      = fs.String("bin", "", "aphrollo binary the hooks invoke (default: this executable)")
		cargoShimDir = fs.String("cargo-shim-dir", "", "dir for the cargo-queue shim (default: ~/.local/share/aphrollo/cargo-queue on Linux/macOS, alongside --bin on Windows)")
		gitHooksDir  = fs.String("git-hooks-dir", "", "git hooks dir for the global gate (default: $XDG_CONFIG_HOME/git/hooks or ~/.config/git/hooks)")
		noGit        = fs.Bool("no-git", false, "skip the git pre-commit gate; wire session hooks only")
		repo         = fs.String("repo", ".", "repo whose CLAUDE.md and .ratchet/README.md init may write (default: the working directory's)")
		claudeMD     = fs.Bool("claude-md", false, "write the managed CLAUDE.md block even when the repo has no CLAUDE.md yet")
		ratchetDoc   = fs.Bool("ratchet-readme", false, "write .ratchet/README.md even when the repo has no laws yet")
		uninstall    = fs.Bool("uninstall", false, "remove the hooks instead of installing them")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := *configDir
	if dir == "" {
		dir = defaultClaudeDir()
	}
	binName := *binPath
	if binName == "" {
		binName = defaultBinPath()
	}
	// Proven before the first write (issue #681, and see gateinit_bin.go):
	// a bad --bin must cost a refusal, never a half-installed gate. The
	// uninstall path is exempt on purpose — removing hooks that point at a
	// binary which is already gone is exactly what a box in this state
	// needs, and a guard standing in front of that would be the one thing
	// worse than not having it.
	if !*uninstall {
		resolved, berr := resolveHookBin(binName, stdout)
		if berr != nil {
			fmt.Fprintf(stderr, "aphrollo gate init: %v\n", berr)
			return 1
		}
		binName = resolved
	}

	changed, err := tdd.InitSettings(dir, binName, *uninstall)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	// The hook scripts this binary replaced go with the settings that pointed
	// at them: a leftover entry double-fired every event, and a retired
	// statusline script reports on a gate that is no longer installed.
	if removed, perr := tdd.PruneRetiredHooks(dir); perr != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", perr)
	} else {
		for _, name := range removed {
			fmt.Fprintf(stdout, "aphrollo gate: removed the retired hook %s\n", filepath.Join(dir, "hooks", name))
		}
	}

	path := filepath.Join(dir, "settings.json")
	switch {
	case !changed:
		fmt.Fprintf(stdout, "aphrollo gate: session hooks already up to date in %s\n", path)
	case *uninstall:
		fmt.Fprintf(stdout, "aphrollo gate: removed session hooks from %s\n", path)
	default:
		fmt.Fprintf(stdout, "aphrollo gate: wired session hooks in %s\n", path)
	}

	// The procedure the gate assumes — RED→GREEN, what makes a test worth
	// keeping, evidence before a completion claim — ships with the binary
	// that enforces it, as a user-level skill, so the two cannot drift and
	// no plugin install is a prerequisite.
	skill := filepath.Join(dir, "skills", "tdd", "SKILL.md")
	sddSkill := filepath.Join(dir, "skills", "sdd", "SKILL.md")
	if *uninstall {
		removed, err := tdd.RemoveTDDSkill(dir)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		if removed {
			fmt.Fprintf(stdout, "aphrollo gate: removed the tdd skill from %s\n", skill)
		}
		sremoved, err := tdd.RemoveSDDSkill(dir)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		if sremoved {
			fmt.Fprintf(stdout, "aphrollo gate: removed the sdd skill from %s\n", sddSkill)
		}
	} else {
		schanged, err := tdd.WriteTDDSkill(dir)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		if schanged {
			fmt.Fprintf(stdout, "aphrollo gate: wrote the tdd skill in %s\n", skill)
		}
		// The feature-level procedure: how a spec becomes lanes, how a lane
		// becomes a commit, and where the transient tree goes at the end.
		sdchanged, err := tdd.WriteSDDSkill(dir)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		if sdchanged {
			fmt.Fprintf(stdout, "aphrollo gate: wrote the sdd skill in %s\n", sddSkill)
		}
	}

	// The three agents the gate's conduct assumes exist. Managed like the
	// skills: refreshed when the binary's copy moves on, and removed on
	// uninstall only when the file still carries the marker this tool wrote.
	if *uninstall {
		removed, err := tdd.RemoveAgents(dir)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		for _, name := range removed {
			fmt.Fprintf(stdout, "aphrollo gate: removed the %s agent from %s\n", name, filepath.Join(dir, "agents", name+".md"))
		}
	} else {
		written, err := tdd.WriteAgents(dir)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		for _, name := range written {
			fmt.Fprintf(stdout, "aphrollo gate: wrote the %s agent in %s\n", name, filepath.Join(dir, "agents", name+".md"))
		}
	}

	if *noGit {
		return 0
	}
	gdir := *gitHooksDir
	if gdir == "" {
		gdir = defaultGitHooksDir()
	}
	gchanged, err := tdd.InitGitGate(gdir, binName, *uninstall)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	switch {
	case !gchanged:
		fmt.Fprintf(stdout, "aphrollo gate: git gate already up to date (%s)\n", gdir)
	case *uninstall:
		fmt.Fprintf(stdout, "aphrollo gate: removed git gate from %s\n", gdir)
	default:
		fmt.Fprintf(stdout, "aphrollo gate: installed git gate in %s (core.hooksPath)\n", gdir)
	}

	// The batch shims are RETIRED under both modes: cmd.exe strips `^` from
	// an argument (which is how `git rev-parse MERGE_HEAD^{tree}` became
	// `HEAD{tree}`) and re-splits quoted ones, so leaving one in place is
	// worse than having no shim at all.
	shimDir := *cargoShimDir
	if shimDir == "" {
		shimDir = defaultCargoShimDir(binName)
	}
	if removed, rerr := tdd.RemoveCmdShims(shimDir); rerr != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", rerr)
	} else {
		for _, name := range removed {
			fmt.Fprintf(stdout, "aphrollo gate: removed the retired batch shim %s\n", filepath.Join(shimDir, name))
		}
	}

	// cargo-queue shim (task A7): a machine-wide dir a session can prepend
	// to its OWN PATH so a DIRECT `cargo` invocation also queues behind the
	// same machine-wide build lock the hooks/gates use, instead of silently
	// waiting on cargo's OWN build-dir lock with zero visibility. --uninstall
	// deliberately does NOT remove it -- a session may still have it
	// prepended to PATH, and leaving a shim in place is harmless (unlike a
	// git hook, nothing fires it automatically).
	if !*uninstall {
		cdir := shimDir
		cchanged, cerr := tdd.InstallCargoShim(cdir, binName)
		switch {
		case cerr != nil:
			warnShimSkipped(stderr, cdir, cerr)
		case cchanged:
			fmt.Fprintf(stdout, "aphrollo gate: installed cargo-queue shim in %s\n", cdir)
		default:
			fmt.Fprintf(stdout, "aphrollo gate: cargo-queue shim already up to date (%s)\n", cdir)
		}

		// git-queue shim (task A11): SAME queue dir as the cargo shim above
		// -- a session prepends ONE dir to PATH and gets both `cargo` and
		// `git` queued. Same --uninstall reasoning as cargo: never removed,
		// harmless to leave in place.
		gchanged2, gerr := tdd.InstallGitShim(cdir, binName)
		switch {
		case gerr != nil:
			warnShimSkipped(stderr, cdir, gerr)
		case gchanged2:
			fmt.Fprintf(stdout, "aphrollo gate: installed git-queue shim in %s\n", cdir)
		default:
			fmt.Fprintf(stdout, "aphrollo gate: git-queue shim already up to date (%s)\n", cdir)
		}

		// The Windows half of both shims: executable COPIES of this binary,
		// which receive the caller's argv verbatim and dispatch on the name
		// they were invoked under. A copy that is currently running cannot be
		// replaced; that is reported and the old copy keeps working.
		exes, eerr := tdd.InstallShimExes(cdir, binName)
		if eerr != nil {
			warnShimSkipped(stderr, cdir, eerr)
		}
		for _, name := range exes.Installed {
			fmt.Fprintf(stdout, "aphrollo gate: installed the %s shim in %s\n", name, cdir)
		}
		for _, name := range exes.Locked {
			fmt.Fprintf(stderr, "aphrollo gate: %s is in use and was left at its old version (%s)\n", name, cdir)
		}

		// The operating instructions belong in the one file a session always
		// reads. A repo that keeps a CLAUDE.md gets the block automatically;
		// one that does not is left alone unless asked with --claude-md. The
		// repo is NAMED (--repo, default the working directory's), because
		// editing a source file as a side effect of where the shell happens to
		// stand is a surprise, and an unnamed one.
		if root := tdd.RepoRoot(*repo); root != "" {
			changed, err := tdd.WriteClaudeMD(root, cdir, *claudeMD)
			switch {
			case errors.Is(err, tdd.ErrManagedBlockInPrimary):
				fmt.Fprintf(stdout, "gate init: CLAUDE.md managed block is behind the template in the merge-only primary; land it through a lane (aphrollo install --repo <lane>)\n")
			case err != nil:
				fmt.Fprintf(stderr, "aphrollo: %v\n", err)
				return 1
			case changed:
				fmt.Fprintf(stdout, "aphrollo gate: wrote the managed block in %s\n", filepath.Join(root, "CLAUDE.md"))
			default:
				fmt.Fprintf(stdout, "aphrollo gate: managed block already up to date in %s\n", filepath.Join(root, "CLAUDE.md"))
			}
			// The law schema belongs beside the laws, so a repo's own docs can
			// cite it instead of a path on the machine that installed this.
			wrote, err := tdd.WriteRatchetReadme(root, *ratchetDoc)
			switch {
			case err != nil:
				fmt.Fprintf(stderr, "aphrollo: %v\n", err)
				return 1
			case wrote:
				fmt.Fprintf(stdout, "aphrollo gate: wrote the law spec in %s\n", filepath.Join(root, ".ratchet", "README.md"))
			}
		}
	}
	return 0
}

// defaultGitHooksDir is where the global git gate's shims live:
// $XDG_CONFIG_HOME/git/hooks, else ~/.config/git/hooks.
func defaultGitHooksDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git", "hooks")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "git", "hooks")
	}
	return filepath.Join(home, ".config", "git", "hooks")
}

// defaultClaudeDir resolves the Claude config dir the way the CLI hooks do:
// $CLAUDE_CONFIG_DIR if set, else ~/.claude.
func defaultClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// defaultCargoShimDir resolves the queue-shim directory `aphrollo install`
// writes into when --cargo-shim-dir is not given. It used to be derived from
// --bin's own directory unconditionally, which on this box is
// /opt/aphrollo-cli/releases/<ts>-<sha>/ — owned by the github-runner account
// that deploys it, not by the operator running install — and the default
// aborted with `mkdir .../cargo-queue: permission denied`. The dir every
// session's PATH is actually configured to prepend
// (~/.local/share/aphrollo/cargo-queue, which aphrollo-infra pins by hand for
// this exact reason) is always writable by the account running install, so
// that is the default on Linux/macOS regardless of where the binary lives.
//
// Windows keeps the old convention: self-install already places the binary
// under the user's own bin dir (e.g. C:/Users/<user>/bin/aphrollo.exe), so
// the sibling cargo-queue dir is already writable and per-user there — the
// managed CLAUDE.md block's own example.
func defaultCargoShimDir(bin string) string {
	if binGOOS == "windows" {
		return filepath.Join(filepath.Dir(bin), "cargo-queue")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "aphrollo", "cargo-queue")
	}
	return filepath.Join(filepath.Dir(bin), "cargo-queue")
}

// warnShimSkipped reports a queue-shim directory init could not write, WITHOUT
// failing init. The shims are opt-in (a session prepends the dir to its own
// PATH); the session hooks and the git gate are what init is actually for, and
// both are already done by the time this runs. Exiting non-zero here aborts
// whatever drives init — an ansible task with `become_user` and a system-wide
// --bin lands on a root-owned bin dir and takes the whole play down with
// `mkdir /usr/local/bin/cargo-queue: permission denied`, despite the hooks and
// gate having been wired correctly. Name the dir and the flag so the fix is
// obvious from the warning alone.
func warnShimSkipped(stderr io.Writer, dir string, err error) {
	fmt.Fprintf(stderr, "aphrollo tdd: skipped queue shims in %s: %v\n", dir, err)
	fmt.Fprintf(stderr, "aphrollo tdd: session hooks and git gate are installed; "+
		"pass --cargo-shim-dir <writable dir> to install the opt-in cargo/git queue shims\n")
}
