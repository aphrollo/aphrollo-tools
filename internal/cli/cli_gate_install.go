package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateInstall writes the git-hook shims into a single repo, or with --dry
// prints the plan and stops.
func runGateInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo = fs.String("repo", ".", "repository to install the hooks into")
		bin  = fs.String("bin", "", "aphrollo binary the repo's own git-hook shims invoke (default: this executable)")
		mut  = addMutFlags(fs)
	)
	pos, err := mut.parse(fs, "gate install", args, stderr)
	if err != nil || refuseArgs("gate install", pos, stderr) {
		return 2
	}
	apply := mut.execute()

	root := tdd.RepoRoot(*repo)
	if root == "" {
		fmt.Fprintf(stderr, "aphrollo: %s is not inside a git repository\n", *repo)
		return 1
	}
	binPath := *bin
	if binPath == "" {
		binPath = defaultBinPath()
	}
	if apply && refuseUnstableDefaultBin(*bin, binPath, "gate install", stderr) {
		return 1
	}
	plan, err := tdd.BuildInstallPlan(root, binPath)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, plan.Render(apply))
	if !apply {
		return 0
	}
	if err := plan.Apply(); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}
