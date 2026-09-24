// Command flakehunt reads a `go test -json` transcript (default: stdin) from
// a `-race -shuffle=on -count=N` run, and files one GitHub issue per test
// that failed at least once — or comments on the existing open one, if this
// same test already has one. It exits nonzero ONLY on an infrastructure
// failure: the build itself broke, the stream did not parse, or `gh` itself
// failed — never because a test flaked. Filing the issues IS this job's
// output; a flake is not this job's own failure.
//
//	go test ./... -race -shuffle=on -count=5 -json | flakehunt -run-url "$RUN_URL"
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("flakehunt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "-", "the go test -json transcript to read; \"-\" reads stdin")
	repo := fs.String("repo", ".", "the checkout whose GitHub remote issues are opened against")
	runURL := fs.String("run-url", "", "this workflow run's own URL, quoted in every issue")
	// quality is the existing theme label closest to flakiness this repo's
	// GitHub already carries: it labels the gate/test-infra issues (mutation
	// coverage gaps, gate false positives) that a flake is one more instance
	// of, and this repo declares no issue-labels list narrower than that in
	// aphrollo.toml.
	label := fs.String("label", "quality", "the theme label filed issues carry")
	newLabel := fs.Bool("new-label", false, "admit a label this repo has not declared")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	r := stdin
	if *input != "-" {
		f, err := os.Open(*input)
		if err != nil {
			fmt.Fprintf(stderr, "flakehunt: %v\n", err)
			return 1
		}
		defer f.Close()
		r = f
	}

	res, err := Parse(r)
	if err != nil {
		fmt.Fprintf(stderr, "flakehunt: reading the go test -json transcript: %v\n", err)
		return 1
	}

	if len(res.BuildFailures) > 0 {
		fmt.Fprintln(stderr, "flakehunt: the build itself failed — not a flake, filing nothing:")
		for _, pkg := range res.BuildFailures {
			fmt.Fprintf(stderr, "  %s\n", pkg)
		}
		return 1
	}

	if len(res.Failures) == 0 {
		fmt.Fprintln(stdout, "flakehunt: every test passed every count — nothing to file")
		return 0
	}

	root := tdd.RepoRoot(*repo)
	if root == "" {
		root = *repo
	}
	filer := Filer{Repo: root, RunURL: *runURL, Label: *label, NewLabel: *newLabel}

	failed := false
	for _, f := range res.Failures {
		url, updated, err := filer.File(f)
		if err != nil {
			fmt.Fprintf(stderr, "flakehunt: filing %s: %v\n", TitleFor(f), err)
			failed = true
			continue
		}
		if updated {
			fmt.Fprintf(stdout, "updated the existing issue for %s\n", TitleFor(f))
		} else {
			fmt.Fprintf(stdout, "filed %s: %s\n", TitleFor(f), url)
		}
	}
	if failed {
		return 1
	}
	return 0
}
