package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// aphrollo update is self-install's sibling for the ordinary case: the box
// running the binary is not necessarily sitting in a checkout of this repo
// at the commit it wants, or a clean one. So it fetches the remote, builds
// from a DETACHED worktree at <remote>/<branch> — never the working tree,
// which may be behind or carrying an edit of its own — and shares
// self-install's swapBinary for the part that replaces the running binary.
const updateUsage = `usage: aphrollo update [--repo DIR] [--bin PATH] [--remote NAME] [--branch NAME] [--no-init]

Fetches <remote>/<branch>, builds ./cmd/aphrollo from a detached temporary
worktree at that commit (never the working tree, which may be behind or
dirty), swaps it in for --bin the same way gate self-install does, sweeps
stale copies beside it, then runs gate init UNDER THE NEW BINARY (so the
managed files come from its templates, not the outgoing build's) unless
--no-init.
`

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
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := checkAphrolloModule(*repo); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: --repo: %v\n", err)
		return 2
	}

	bin := resolveBinPath(*binPath, "aphrollo update", stdout)

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

	remoteBranch := *remote + "/" + *branch
	if _, err := runGit("fetch", *remote); err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	head, err := runGit("rev-parse", remoteBranch)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}

	if commit, _, stamped := buildinfo.Stamp(); stamped && commit == head {
		fmt.Fprintf(stdout, "aphrollo update: already at %s [skip]\n", shortSHA(head))
		return 0
	}

	tmp, err := os.MkdirTemp("", "aphrollo-update-*")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo update: %v\n", err)
		return 1
	}
	if _, err := runGit("worktree", "add", "--detach", tmp, remoteBranch); err != nil {
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

	if *noInit {
		return 0
	}
	return initAfterSwap("aphrollo update", bin, fs.Args(), stdout, stderr)
}

// shortSHA reports the first 7 characters of a full commit sha, the width
// `aphrollo version` and gate self-install already use to name a build.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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
