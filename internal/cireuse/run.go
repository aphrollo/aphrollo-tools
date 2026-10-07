// Package cireuse decides whether a push to trunk may skip the heavy test jobs
// because the merged pull request's own pipeline run already tested the same
// tree and passed. It prints `reuse=true` or `reuse=false` on stdout, ready
// for $GITHUB_OUTPUT, and the reason on stderr. It exits 0 for either answer
// and 2 for a command line it refuses, printing no answer then: the workflow
// reads a missing answer as false, so every doubt runs the full suite.
//
//	aphrollo ci reuse -repo o/r -sha "$GITHUB_SHA" -tree "$(git rev-parse HEAD^{tree})" \
//	  -workflow .github/workflows/pipeline.yml -require 'test=Test (race'
package cireuse

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// Main is `aphrollo ci reuse`: run over the gh CLI, which carries the job's
// GH_TOKEN. It returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, func(repo, workflow string) Source {
		return newGHSource(repo, workflow)
	})
}

// requirements is a repeatable -require job=step-prefix flag.
type requirements []Requirement

func (r *requirements) String() string { return fmt.Sprint(*r) }

func (r *requirements) Set(v string) error {
	job, prefix, ok := strings.Cut(v, "=")
	if !ok || job == "" || prefix == "" {
		return fmt.Errorf("%q is not job=step-prefix", v)
	}
	*r = append(*r, Requirement{Job: job, StepPrefix: prefix})
	return nil
}

func run(args []string, stdout, stderr io.Writer, source func(repo, workflow string) Source) int {
	fs := flag.NewFlagSet("ci reuse", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "owner/name of the repository")
	sha := fs.String("sha", "", "the pushed commit")
	tree := fs.String("tree", "", "the tree of the pushed commit")
	workflow := fs.String("workflow", "", "path of the pipeline workflow file")
	var reqs requirements
	fs.Var(&reqs, "require", "job=step-prefix: a job, and the step of it that must have run to success (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repo == "" || *sha == "" || *tree == "" || *workflow == "" {
		fmt.Fprintln(stderr, "aphrollo ci reuse: -repo, -sha, -tree and -workflow are all required")
		return 2
	}
	v := Decide(source(*repo, *workflow), Commit{Repo: *repo, SHA: *sha, Tree: *tree, Workflow: *workflow}, reqs)
	fmt.Fprintf(stderr, "cireuse: %s\n", v.Reason)
	fmt.Fprintf(stdout, "reuse=%t\n", v.Reuse)
	return 0
}
