package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/release"
	"github.com/aphrollo/aphrollo-tools/internal/run"
)

const versionCheckUsage = `usage: aphrollo version check --base <ref> --body-file <file> [--repo <dir>]

Hold a change to the version rule, in the checkout of this repo. A PR carries
the level of release it asks for, never a version number: the PR body
(--body-file) must carry one line, "version: none|patch|minor|major", and a PR
that is not "none" adds exactly one changelog.d/<slug>.md fragment, whose first
line "level: patch|minor|major" agrees with it; a "none" PR adds none. The
change is what the branch did since it left the merge base of <ref> and HEAD (a
base that moved on since the branch forked is not the branch's change). The PR
must not add or edit internal/buildinfo/VERSION (deleting it is fine), must not
change a released section of CHANGELOG.md, and must not edit or delete a
fragment already merged. A change to a law preset, a language row or a mask
must be a minor change at least. Every failure is printed on its own line and
the exit is 1. The release tag itself is made on main from the fragments
merged: see aphrollo release plan.
`

func runVersionCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, versionCheckUsage) }
	base := fs.String("base", "", "the ref the change is measured from (required)")
	bodyFile := fs.String("body-file", "", "a file holding the PR body (required)")
	repo := fs.String("repo", ".", "a directory inside the checkout to judge")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *base == "" || *bodyFile == "" || fs.NArg() != 0 {
		fmt.Fprint(stderr, versionCheckUsage)
		return 2
	}
	problems, summary, err := judgeVersionChange(*repo, *base, *bodyFile)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo version check: %v\n", err)
		return 1
	}
	for _, p := range problems {
		fmt.Fprintf(stderr, "version: %s\n", p)
	}
	if len(problems) > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "version: ok (%s)\n", summary)
	return 0
}

// judgeVersionChange gathers what the rule judges: the body, the files the
// branch touched since it forked, the fragments it added and the changelog on
// both sides. An error is a check that could not be made, never a verdict.
func judgeVersionChange(repo, base, bodyFile string) (problems []string, summary string, err error) {
	root := compat.RepoRoot(repo)
	if root == "" {
		return nil, "", fmt.Errorf("%s is not inside a git repository", repo)
	}
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		return nil, "", err
	}
	if _, err := gitStdoutIn(root, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		return nil, "", fmt.Errorf("%q is not a commit in this repository: %w", base, err)
	}
	// The change is what the branch did since it left base, not what base did
	// since: a branch behind its base is measured from the merge base.
	forkOut, err := gitStdoutIn(root, "merge-base", base, "HEAD")
	if err != nil {
		return nil, "", fmt.Errorf("no merge base between %q and HEAD: %w", base, err)
	}
	fork := strings.TrimSpace(forkOut)
	changed, err := gitStdoutIn(root, "diff", "--name-status", "-z", "--no-renames", fork, "HEAD")
	if err != nil {
		return nil, "", err
	}
	files, err := parseNameStatus(changed)
	if err != nil {
		return nil, "", err
	}
	change := release.Change{Body: string(body), Files: files, Fragments: map[string]string{}}
	var added []string
	for _, f := range files {
		if _, isFragment := release.FragmentName(f.File); f.Status == 'A' && isFragment {
			text, err := gitStdoutIn(root, "show", "HEAD:"+f.File)
			if err != nil {
				return nil, "", err
			}
			change.Fragments[f.File] = text
			added = append(added, f.File)
		}
	}
	if change.BaseChangelog, err = fileAtRef(root, fork, compat.ChangelogFile); err != nil {
		return nil, "", err
	}
	if change.HeadChangelog, err = fileAtRef(root, "HEAD", compat.ChangelogFile); err != nil {
		return nil, "", err
	}
	problems = release.JudgeChange(change)
	declared, _ := compat.DeclaredBump(string(body)) // a body with no decision is a problem above, and no summary is printed
	return problems, release.Summary(declared, added), nil
}

// parseNameStatus reads `git diff --name-status -z --no-renames`: a status
// letter and a path, each NUL-terminated.
func parseNameStatus(out string) ([]release.FileChange, error) {
	fields := strings.FieldsFunc(out, func(r rune) bool { return r == 0 })
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("git diff --name-status gave %d fields, want status and file pairs", len(fields))
	}
	var files []release.FileChange
	for i := 0; i < len(fields); i += 2 {
		status := fields[i]
		if len(status) != 1 {
			return nil, fmt.Errorf("git diff --name-status gave status %q for %s", status, fields[i+1])
		}
		files = append(files, release.FileChange{Status: status[0], File: fields[i+1]})
	}
	return files, nil
}

// fileAtRef is the text of file at ref, "" when ref has no such file: a repo
// before its first changelog has no released section to protect.
func fileAtRef(root, ref, file string) (string, error) {
	listed, err := gitStdoutIn(root, "ls-tree", "--name-only", ref, "--", file)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(listed) == "" {
		return "", nil
	}
	return gitStdoutIn(root, "show", ref+":"+file)
}

// gitStdoutIn runs git in dir and returns its stdout; a failure carries git's own
// stderr, so the one line a caller prints says why.
func gitStdoutIn(dir string, args ...string) (string, error) {
	var errb bytes.Buffer
	out, err := lightOutput(run.Spec{Name: "git", Args: append([]string{"-C", dir}, args...), Stderr: &errb})
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return string(out), nil
}
