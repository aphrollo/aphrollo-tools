// Command tddsplit carves the internal/tdd package into layered packages from
// a file-to-package manifest (manifest.txt beside it).
//
//	go run ./tools/tddsplit -levels L0,L1            move, generate aliases, verify
//	go run ./tools/tddsplit -levels L0,L1 -report    analyse and print only
//	go run ./tools/tddsplit -regen                   regenerate export.go/deps_*.go/api_*.go
//	                                                  from the working tree, no commit needed
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	manifest := flag.String("manifest", "tools/tddsplit/manifest.txt", "file-to-package manifest, relative to the repo root")
	levels := flag.String("levels", "", "comma-separated levels to carve out, e.g. L0,L1")
	report := flag.Bool("report", false, "analyse and print the plan and every report; touch nothing")
	noVerify := flag.Bool("no-verify", false, "skip go build, linux and windows vet and golangci-lint after the move")
	regen := flag.Bool("regen", false, "regenerate export.go/deps_*.go/api_*.go from the working tree as it sits now; no clean-tree requirement, no commit, writes only the generated files; refuses a hand-edited one")
	flag.Parse()
	if err := validateFlags(*regen, *levels, *report); err != nil {
		fmt.Fprintf(os.Stderr, "tddsplit: %v\n", err)
		os.Exit(2)
	}
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tddsplit: not inside a git work tree: %v\n", err)
		os.Exit(1)
	}
	repo := strings.TrimSpace(string(top))
	m := *manifest
	if !filepath.IsAbs(m) {
		m = filepath.Join(repo, filepath.FromSlash(m))
	}
	if *regen {
		if err := regenerate(Options{Repo: repo, Manifest: m, Out: os.Stdout}); err != nil {
			fmt.Fprintf(os.Stderr, "tddsplit: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(Options{Repo: repo, Manifest: m, Levels: *levels, Report: *report, Verify: !*noVerify, Out: os.Stdout}); err != nil {
		fmt.Fprintf(os.Stderr, "tddsplit: %v\n", err)
		os.Exit(1)
	}
}

// validateFlags checks the -regen / -levels / -report combination before any
// git or filesystem work: -regen always regenerates every already-split
// level (see allLevels in regen.go), so it takes neither -levels nor
// -report; the original mode still requires -levels.
func validateFlags(regen bool, levels string, report bool) error {
	if regen {
		if levels != "" || report {
			return fmt.Errorf("-regen takes no -levels or -report; it always regenerates every already-split level")
		}
		return nil
	}
	if levels == "" {
		return fmt.Errorf("-levels is required")
	}
	return nil
}
