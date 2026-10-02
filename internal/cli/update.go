package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/rollback"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// aphrollo update is the ONLY way the box binary moves. The box running it
// is not necessarily sitting in a checkout of this repo at the commit it
// wants, or a clean one, so it fetches the remote and builds from a DETACHED
// worktree at the commit it resolved — never the working tree, which may be
// behind or carrying an edit of its own. `gate self-install`, which built from
// an arbitrary checkout and could therefore point the box at unmerged code, was
// retired with the bootstrap that needed it (#659, #673).
const updateUsage = `usage: aphrollo update [--repo DIR] [--bin PATH] [--remote NAME] [--branch NAME] [--to REF | --unpin] [--no-init] [--dry]

Fetches <remote>/<branch>, builds ./cmd/aphrollo from a detached temporary
worktree at that commit (never the working tree, which may be behind or
dirty), swaps it in for --bin, then runs gate init UNDER THE NEW BINARY (so
the managed files come from its templates, not the outgoing build's) unless
--no-init. The last 3 binaries stay beside --bin (the installed one counted),
each with its commit and build stamp recorded; older copies are reclaimed
unless something is still running them.

--to REF installs a tag (v1.3.0) or a commit sha that is already merged on
<remote>/<branch>, and pins the box to it: a plain update then says it is
pinned and does nothing, until --unpin clears the pin and returns to
<remote>/<branch>. When a kept binary was built from the wanted commit, it is
switched to without a build. Every swap, pin and unpin is written to the event
log. Idempotent: a box already at the wanted commit reports [skip].

--dry prints the ref it would fetch, what it would build and the path it would
swap, the pin change and the binaries kept, and stops before the fetch.

This is the only command that replaces the installed binary: gate
self-install, which built from an arbitrary checkout, is retired.
`

// installWritable is a seam over tdd.InstallWritable so a test can force
// the "not writable" branch without relying on real permission bits — on an
// account that bypasses them (root), a chmod-based fixture can never produce
// the refusal it means to pin.
//
// Not tdd.binaryInstallWritable: that seam takes no directory — it always
// probes os.Executable()'s own dir, the running binary's. This check is of
// filepath.Dir(bin), which --bin can point anywhere else entirely; reusing
// the no-arg seam here would silently ignore --bin and probe the wrong
// directory.
var installWritable = tdd.InstallWritable

func runUpdate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, updateUsage)
		return 0
	}
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo    = fs.String("repo", ".", "aphrollo-tools checkout whose remote to fetch from (the build runs in a temporary worktree)")
		binPath = fs.String("bin", "", "binary to replace (default: this executable)")
		noInit  = fs.Bool("no-init", false, "replace the binary only; skip `gate init`")
		remote  = fs.String("remote", "origin", "remote to fetch and build from")
		branch  = fs.String("branch", "main", "branch to build")
		dry     = fs.Bool("dry", false, "print the ref, build target and swap path and stop before the fetch")
		to      = fs.String("to", "", "install this tag or commit sha (merged on <remote>/<branch>) and pin the box to it")
		unpin   = fs.Bool("unpin", false, "clear the pin and return to <remote>/<branch>")
	)
	// Everything after a bare "--" is forwarded to `gate init` untouched; what
	// comes before it is this verb's own flags, read wherever they sit.
	var forwarded []string
	if i := slices.Index(args, "--"); i >= 0 {
		forwarded = args[i+1:]
		args = args[:i]
	}
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil || refuseArgs("update", pos, stderr) {
		return 2
	}
	if *to != "" && *unpin {
		fmt.Fprintln(stderr, "aphrollo update: --to and --unpin are exclusive: --to moves the pin, --unpin removes it")
		return 2
	}
	if strings.HasPrefix(*to, "-") {
		fmt.Fprintf(stderr, "aphrollo update: --to: %q looks like an option, not a tag or commit sha\n", *to)
		return 2
	}

	if err := checkAphrolloModule(*repo); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: --repo: %v\n", err)
		return 2
	}

	remoteBranch := *remote + "/" + *branch
	pin, pinState := rollback.ReadPin()
	if code, held := holdForPin(stdout, stderr, pinState, pin, *to, *unpin, remoteBranch); held {
		return code
	}

	bin := resolveBinPath(*binPath, "aphrollo update", stdout)

	// Checked before the fetch and build run at all: os.Executable (what
	// resolveBinPath falls back to) resolves every symlink, so on the box
	// that deploys via CI (deploy/deploy-prod.sh) this lands on
	// /opt/aphrollo-cli/releases/<ts>-<sha>/aphrollo, a directory only the
	// deploy pipeline's own account owns. Finding that out here means
	// "permission denied" never comes out of `go build` after a wasted
	// fetch and worktree checkout.
	if !installWritable(filepath.Dir(bin)) {
		if owner := tdd.InstallOwner(filepath.Dir(bin)); owner != "" {
			fmt.Fprintf(stderr, "aphrollo update: %s is not writable by this account — it is owned by %s and is deployed by the repo pipeline on merge, not by aphrollo update here\n", bin, owner)
		} else {
			fmt.Fprintf(stderr, "aphrollo update: %s is not writable by this account — it is deployed by the repo pipeline on merge, not by aphrollo update here\n", bin)
		}
		return 1
	}

	installs := rollback.OpenInstalls(filepath.Dir(bin), filepath.Base(bin))
	if *dry {
		dryRun{repo: *repo, remote: *remote, branch: *branch, bin: bin, to: *to, unpin: *unpin,
			state: pinState, pin: pin, kept: installs.Others(filepath.Base(bin))}.print(stdout)
		return 0
	}

	git, err := resolveRealGit()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	runGit := func(args ...string) (string, error) {
		cmd := exec.Command(git, append([]string{"-C", *repo}, args...)...)
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			said := strings.TrimSpace(errb.String())
			if said == "" {
				said = err.Error()
			}
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), said)
		}
		return strings.TrimSpace(out.String()), nil
	}

	// A plain fetch also brings every tag that points into the history it
	// fetches, which is every tag --to may name: it only installs what is
	// merged on <remote>/<branch>.
	if _, err := runGit("fetch", *remote); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	target, err := resolveUpdateTarget(runGit, *to, remoteBranch)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}

	run := &updateRun{bin: bin, remoteBranch: remoteBranch, target: target, installs: installs, stdout: stdout, stderr: stderr}
	if commit, _, stamped := buildinfo.Stamp(); stamped && commit == target.Commit {
		fmt.Fprintf(stdout, "aphrollo update: already at %s [skip]\n", target.describe())
		return run.settlePin(*to, *unpin, pinState, pin)
	}
	if code := run.install(runGit); code != 0 {
		return code
	}
	if code := run.settlePin(*to, *unpin, pinState, pin); code != 0 {
		return code
	}

	if *noInit {
		return 0
	}
	return initAfterSwap("aphrollo update", bin, forwarded, stdout, stderr)
}

// shortSHA reports the first 7 characters of a full commit sha, the width
// `aphrollo version` already uses to name a build.
func shortSHA(sha string) string {
	return rollback.ShortSHA(sha)
}

// checkAphrolloModule reports an error unless repo's go.mod declares module
// github.com/aphrollo/aphrollo-tools — the check that keeps `update` from
// being pointed at some unrelated checkout and building whatever
// ./cmd/aphrollo happens to mean there. go.mod permits blank lines and `//`
// comments before the module directive, so this skips those before looking
// for the directive on the first line that is neither.
func checkAphrolloModule(repo string) error {
	const want = "github.com/aphrollo/aphrollo-tools"
	// One reader of the module directive, in the package the fixtures stage
	// asks the same question from (`is this the checkout that compiles the
	// matchers`) — two copies of this parse would drift one fix at a time.
	path, err := tdd.ModulePath(repo)
	if err != nil {
		return fmt.Errorf("%s does not look like this module: %w", repo, err)
	}
	if path != want {
		return fmt.Errorf("%s does not look like %s (go.mod says %q)", repo, want, path)
	}
	return nil
}
