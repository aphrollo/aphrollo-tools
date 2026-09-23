// Command tddsplit carves the internal/tdd package into layered packages from
// a file-to-package manifest (manifest.txt beside it).
//
//	go run ./tools/tddsplit -levels L0,L1            move, generate aliases, verify
//	go run ./tools/tddsplit -levels L0,L1 -report    analyse and print only
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
	flag.Parse()
	if *levels == "" {
		fmt.Fprintln(os.Stderr, "tddsplit: -levels is required")
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
	if err := run(Options{Repo: repo, Manifest: m, Levels: *levels, Report: *report, Verify: !*noVerify, Out: os.Stdout}); err != nil {
		fmt.Fprintf(os.Stderr, "tddsplit: %v\n", err)
		os.Exit(1)
	}
}
