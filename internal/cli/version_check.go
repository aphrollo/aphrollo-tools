package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

const versionCheckUsage = `usage: aphrollo version check --base <ref> --body-file <file> [--repo <dir>]

Hold a change to the version rule, in the checkout of this repo. The PR body
(--body-file) must carry one line, "version: none|patch|minor|major"; the
VERSION file must have moved from <ref> to HEAD by exactly that much; a change
to a law preset, a language row or a mask must be a minor bump at least; and
CHANGELOG.md must have a section for the version in VERSION. Every failure is
printed on its own line and the exit is 1. A <ref> with no VERSION file counts
as 0.0.0, so a repo's first version is a major bump.
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

// judgeVersionChange gathers what the rule judges: the body, the version at
// base and in the checkout, the files the change touches, and the changelog.
// An error is a check that could not be made, never a verdict.
func judgeVersionChange(repo, base, bodyFile string) (problems []string, summary string, err error) {
	root := compat.RepoRoot(repo)
	if root == "" {
		return nil, "", fmt.Errorf("%s is not inside a git repository", repo)
	}
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		return nil, "", err
	}
	head, err := readVersionFile(filepath.Join(root, filepath.FromSlash(compat.VersionFile)))
	if err != nil {
		return nil, "", err
	}
	was, err := versionAtRef(root, base)
	if err != nil {
		return nil, "", err
	}
	changed, err := gitStdoutIn(root, "diff", "--name-only", "-z", base, "HEAD")
	if err != nil {
		return nil, "", err
	}
	problems = compat.JudgeBump(was, head, string(body), strings.FieldsFunc(changed, func(r rune) bool { return r == 0 }))
	changelog, _ := os.ReadFile(filepath.Join(root, compat.ChangelogFile)) // a missing file is the missing section the problem below names
	if !compat.ChangelogHas(string(changelog), head) {
		problems = append(problems, fmt.Sprintf("%s has no section for %s: add a `## %s` heading with what a consumer will notice and what migrates by itself", compat.ChangelogFile, head, head))
	}
	return problems, fmt.Sprintf("%s -> %s", was, head), nil
}

func readVersionFile(file string) (compat.Version, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return compat.Version{}, err
	}
	v, err := compat.ParseVersion(strings.TrimSpace(string(data)))
	if err != nil {
		return compat.Version{}, fmt.Errorf("%s: %w", compat.VersionFile, err)
	}
	return v, nil
}

// versionAtRef is the version the VERSION file carried at ref. A ref that
// never had the file is a repo before its first version: 0.0.0.
func versionAtRef(root, ref string) (compat.Version, error) {
	if _, err := gitStdoutIn(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return compat.Version{}, fmt.Errorf("%q is not a commit in this repository: %w", ref, err)
	}
	listed, err := gitStdoutIn(root, "ls-tree", "--name-only", ref, "--", compat.VersionFile)
	if err != nil {
		return compat.Version{}, err
	}
	if strings.TrimSpace(listed) == "" {
		return compat.Version{}, nil
	}
	text, err := gitStdoutIn(root, "show", ref+":"+compat.VersionFile)
	if err != nil {
		return compat.Version{}, err
	}
	v, err := compat.ParseVersion(strings.TrimSpace(text))
	if err != nil {
		return compat.Version{}, fmt.Errorf("%s at %s: %w", compat.VersionFile, ref, err)
	}
	return v, nil
}

// gitStdoutIn runs git in dir and returns its stdout; a failure carries git's own
// stderr, so the one line a caller prints says why.
func gitStdoutIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return string(out), nil
}
