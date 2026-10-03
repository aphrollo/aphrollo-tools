package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
)

const runTimeout = 15 * time.Minute

// runner starts processes sealed off from the box: no GIT_* variable, a neutral
// global git config, and the gate's state store under area. A tree's two binaries
// share one area, which is how the second reads what the first wrote, and
// nothing a run writes lands in a real home.
type runner struct{ area string }

func (r runner) env() []string { return gitenv.Sealed(os.Environ(), r.area) }

func (r runner) run(b bin, dir, stdin string, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Dir, cmd.Env = dir, r.env()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return res, fmt.Errorf("%s %s timed out after %s", b.Label, strings.Join(args, " "), runTimeout)
	case errors.As(err, &exit):
		res.Exit = exit.ExitCode()
	case err != nil:
		return res, fmt.Errorf("starting %s: %w", b.Path, err)
	}
	return res, nil
}

func (r runner) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, r.env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, firstLines(string(out), 4))
	}
	return string(out), nil
}

// pickPrevious is the newest release tag (tags come newest first) that is not
// the candidate itself: a push to main is replayed before it is tagged, but a
// re-run of a tagged commit still has a release to compare with.
func pickPrevious(tags []string, sha func(tag string) (string, error), head string) (string, error) {
	for _, tag := range tags {
		got, err := sha(tag)
		if err != nil {
			return "", err
		}
		if got != head {
			return tag, nil
		}
	}
	return "", errors.New("no release tag other than the candidate's own commit: fetch tags, or name the release with -prev")
}

// previousRef is the release to replay against: ref when given, else the newest tag.
func (r runner) previousRef(repo, ref string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	out, err := r.git(repo, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	head, err := r.git(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return pickPrevious(strings.Fields(out), func(tag string) (string, error) {
		sha, err := r.git(repo, "rev-list", "-n", "1", tag)
		return strings.TrimSpace(sha), err
	}, strings.TrimSpace(head))
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// build compiles the aphrollo command of the module in dir to out, the way CI
// builds the CLI everywhere else in this repository.
func build(dir, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", out, "./cmd/aphrollo")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building %s: %w: %s", dir, err, firstLines(string(output), 20))
	}
	return nil
}

// buildRelease builds the release at ref from a clone of repo.
func (r runner) buildRelease(repo, ref, src, out string) error {
	if err := os.RemoveAll(src); err != nil {
		return err
	}
	if _, err := r.git(filepath.Dir(src), "clone", "-q", "--no-checkout", repo, src); err != nil {
		return err
	}
	if _, err := r.git(src, "checkout", "-q", "--detach", ref); err != nil {
		return err
	}
	return build(src, out)
}

// files lists the tracked files of dir.
func (r runner) files(dir string) ([]string, error) {
	out, err := r.git(dir, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(out, "\x00"), "\x00"), nil
}

// selfTree is this repository as the candidate checkout holds it, cloned so the
// replay never writes to the checkout.
func (r runner) selfTree(repo, dir string, nfiles int) (tree, error) {
	if _, err := r.git(filepath.Dir(dir), "clone", "-q", repo, dir); err != nil {
		return tree{}, err
	}
	all, err := r.files(dir)
	return tree{Name: "self", Dir: dir, Files: pickFiles(all, nfiles), Store: r.area}, err
}

// The laws the synthetic tree is judged by: the embedded presets for the
// languages it holds, written by the previous release (the laws a consumer has
// already), and two probe laws per language that count every identifier in the
// lexer's two views. A lexer change moves a boundary of a string or a comment
// and so moves a count, on a tree no preset happens to have a hit in.
const hygienePattern = "TODO|FIXME|HACK|XXX"

var probeRows = []struct{ Row, Prefix, Include string }{
	{"typescript", "//", `"**/*.ts", "**/*.tsx"`},
	{"python", "#", `"**/*.py"`},
}

func probeLaw(row, prefix, include, view string, codeOnly bool) string {
	return fmt.Sprintf(`name = "probe_%[2]s_%[1]s"
description = "Replay probe: the identifiers the %[1]s lexer reads, %[2]s."
severity = "warn"
code_only = %[5]t
mask_strings = true
comment_prefix = %[3]q

[scope]
include = [%[4]s]
exclude = ["**/node_modules/**", ".ratchet/**"]

[matcher]
kind = "regex-absent"
pattern = "[A-Za-z_][A-Za-z_0-9]*"
key = "file"
count = "matches"
`, row, view, prefix, include, codeOnly)
}

// syntheticTree writes the synthetic tree, lays prev's laws over it and commits
// the lot, so what the two binaries then read has not changed since.
func (r runner) syntheticTree(prev bin, dir string, seed uint64, lines, nfiles int) (tree, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return tree{}, err
	}
	if _, err := synthesize(dir, seed, lines); err != nil {
		return tree{}, err
	}
	if _, err := r.git(dir, "init", "-q"); err != nil {
		return tree{}, err
	}
	res, err := r.run(prev, dir, "", "ratchet", "init", "--preset", "common,ts",
		"--param", "pattern="+hygienePattern, "--param", `source_include="**/*.ts", "**/*.tsx", "**/*.py"`)
	// init exits 1 when a preset asked for a parameter nobody gave: the ones that
	// need data no tree here has are skipped by it.
	if err != nil || res.Exit > 1 {
		return tree{}, fmt.Errorf("ratchet init: exit %d: %v %s", res.Exit, err, firstLines(res.Stderr+res.Stdout, 4))
	}
	for _, p := range probeRows {
		for i, view := range []string{"code", "view"} {
			text := probeLaw(p.Row, p.Prefix, p.Include, view, i == 0)
			name := filepath.Join(dir, ".ratchet", "laws", "probe_"+view+"_"+p.Row+".toml")
			if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
				return tree{}, err
			}
		}
	}
	if _, err := r.git(dir, "add", "-A"); err != nil {
		return tree{}, err
	}
	if _, err := r.git(dir, "commit", "-q", "-m", "synthetic tree "+strconv.FormatUint(seed, 10)); err != nil {
		return tree{}, err
	}
	all, err := r.files(dir)
	return tree{Name: "synthetic", Dir: dir, Files: pickFiles(all, nfiles), Store: r.area, PrevMustRead: true}, err
}

// untouched is empty when dir holds exactly what git has: a read-only command
// that changed the tree has not only read it.
func (r runner) untouched(dir string) (string, error) {
	out, err := r.git(dir, "status", "--porcelain", "--untracked-files=all")
	return strings.TrimSpace(out), err
}
