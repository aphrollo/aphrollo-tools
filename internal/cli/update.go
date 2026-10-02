package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// aphrollo update is the ONLY way the box binary moves. It follows the newest
// release TAG (v<MAJOR.MINOR.PATCH>), never the tip of main: a tag is made per
// merged PR that bumps internal/buildinfo/VERSION, so what a box runs is a
// version a consumer was told about. The box running it is not necessarily
// sitting in a checkout of this repo at the commit it wants, or a clean one,
// so it fetches the remote's tags and builds from a DETACHED worktree at the
// tag, never the working tree, which may be behind or carrying an edit of its
// own. `gate self-install`, which built from an arbitrary checkout, was
// retired with the bootstrap that needed it (#659, #673).
const updateUsage = `usage: aphrollo update [--repo DIR] [--bin PATH] [--remote NAME] [--no-init] [--dry]

Fetches <remote>'s tags, finds the newest release tag (v<MAJOR.MINOR.PATCH>),
builds ./cmd/aphrollo from a detached temporary worktree at that tag (never
the working tree, which may be behind or dirty), swaps it in for --bin,
sweeps stale copies beside it, then runs gate init UNDER THE NEW BINARY (so
the managed files come from its templates, not the outgoing build's) unless
--no-init. It prints the version it moved from and to, or "[skip] already at
vX". --dry prints what it would fetch, build and swap, and stops before the
fetch.

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
		dry     = fs.Bool("dry", false, "print the tag source, build target and swap path and stop before the fetch")
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

	if err := checkAphrolloModule(*repo); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: --repo: %v\n", err)
		return 2
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

	if *dry {
		fmt.Fprintf(stdout, "aphrollo update (dry run): nothing fetched, built or swapped\n  fetch: %s tags, newest v<MAJOR.MINOR.PATCH> in %s\n  build: ./cmd/aphrollo from a detached worktree at that tag\n  swap:  %s\n",
			*remote, *repo, bin)
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

	if _, err := runGit("fetch", "--tags", *remote); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	tagList, err := runGit("tag", "--list", "v*")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	tag, found := newestReleaseTag(strings.Fields(tagList))
	if !found {
		fmt.Fprintf(stderr, "aphrollo update: %s has no release tag (v<MAJOR.MINOR.PATCH>): a tag is made when a PR that bumps internal/buildinfo/VERSION merges; ask for the release to be tagged\n", *remote)
		return 1
	}
	head, err := runGit("rev-parse", tag+"^{commit}")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}

	if commit, _, stamped := buildinfo.Stamp(); stamped && commit == head {
		fmt.Fprintf(stdout, "aphrollo update: [skip] already at %s\n", tag)
		return 0
	}

	tmp, err := os.MkdirTemp("", "aphrollo-update-*")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	if _, err := runGit("worktree", "add", "--detach", tmp, head); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		_ = os.RemoveAll(tmp)
		return 1
	}
	defer func() {
		_, _ = runGit("worktree", "remove", "--force", tmp)
		_ = os.RemoveAll(tmp)
	}()

	staged := siblingPath(bin, ".new")
	_ = os.Remove(staged)
	desc, err := buildAphrollo(tmp, staged)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: build failed, nothing was replaced\n%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "aphrollo update: build  %s -> %s\n", desc, staged)

	if _, err := swapBinary("aphrollo update", bin, staged, stdout); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "aphrollo update: v%s -> %s\n", buildinfo.Version(), tag)

	if *noInit {
		return 0
	}
	return initAfterSwap("aphrollo update", bin, forwarded, stdout, stderr)
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
