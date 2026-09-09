package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The managed hooks dir is the one directory this tool owns on the whole box:
// `aphrollo install` writes its shims there and points git's global
// core.hooksPath at it. Install has always REFUSED to clobber a hand-written
// hook it finds there (foreignHookExists), which is right — and until issue
// #582 nothing ever said one was there. A `post-merge` hook dated 2026-08-18
// sat in that dir running `git worktree remove --force` on every merge, and
// `aphrollo check` passed every day it did, because doctor judges the shims
// this tool wrote and nothing else. The operator read the destruction as an
// aphrollo defect, which from where they stood is exactly what it looked like.
//
// So doctor names them: every file in that dir git would run and this tool
// did not write. The finding carries what it takes to recognise the file
// without opening it — name, mtime, first comment line — and the remedy is
// the operator's own choice between moving it somewhere git does not run it
// and deleting it, because this tool has no business guessing which.

// foreignHookTimeFormat is how a foreign hook's mtime is rendered: minutes,
// no zone. It is a fact for a human to recognise the file by ("the one from
// August"), not a timestamp anything parses.
const foreignHookTimeFormat = "2006-01-02 15:04"

// foreignHookCommentLimit caps how much of a first comment line is quoted. A
// hook's opening comment is a sentence; a file whose first comment line is
// four kilobytes of minified text would push every other finding off the
// operator's screen, and the point of the quote is recognition, not content.
const foreignHookCommentLimit = 120

// doctorForeignHooks reports every executable file in the managed hooks dir
// this tool did not write. Silent when there is no directory to read: an
// unset, missing or foreign core.hooksPath is doctorGitHooksPath's finding,
// and a second row about the same fact is noise. A dir that exists but
// cannot be read is reported as a WARNING carrying the error rather than a
// failure — an unreadable directory is a fact about the box, and this check
// must never be the thing that blocks a run.
func doctorForeignHooks(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "foreign hooks", OK: true}
	if in.GitHooksPath == "" {
		return c
	}
	entries, err := os.ReadDir(in.GitHooksPath)
	if err != nil {
		if !os.IsNotExist(err) {
			c.Warn = true
			c.Detail = fmt.Sprintf("could not read %s (%v)", in.GitHooksPath, err)
		}
		return c
	}
	var found []string
	for _, e := range entries {
		if f, ok := foreignHookFinding(in.GitHooksPath, e.Name()); ok {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return c
	}
	c.OK = false
	c.Detail = fmt.Sprintf("%s in %s — not written by this tool, and git runs it on every matching event; "+
		"move it out of the managed hooks dir or delete it",
		strings.Join(found, "; "), in.GitHooksPath)
	return c
}

// foreignHookFinding describes one entry of the managed hooks dir when it is
// a hook this tool did not write, ok=false when it is not one at all: a
// subdirectory, one of git's own `*.sample` files (git never runs those, and
// naming nine of them would bury the one file that matters), a
// non-executable file on a platform that HAS an executable bit, or a shim
// carrying this tool's install marker.
//
// A file whose contents cannot be read is reported rather than skipped: the
// marker is what tells a managed shim from a foreign hook, and a file that
// refuses to answer that question is the last thing to pass over in silence.
func foreignHookFinding(dir, name string) (string, bool) {
	if strings.HasSuffix(name, ".sample") {
		return "", false
	}
	path := filepath.Join(dir, name)
	fi, err := os.Stat(path)
	if err != nil || !hookIsExecutable(fi) {
		return "", false
	}
	data, readErr := os.ReadFile(path)
	if readErr == nil && strings.Contains(string(data), installMarker) {
		return "", false // a shim this tool wrote
	}
	detail := firstCommentLine(data)
	if readErr != nil {
		detail = fmt.Sprintf("unreadable: %v", readErr)
	}
	return fmt.Sprintf("%s (%s, %s)", name, fi.ModTime().Format(foreignHookTimeFormat), detail), true
}

// hookGOOSFn names the platform this check judges a file's mode on, read
// through a seam rather than off runtime directly so a test pins the OTHER
// host's branch — the model internal/cli/selfinstall.go's binGOOS set, and
// what the platform_seam law asks for.
var hookGOOSFn = func() string { return runtime.GOOS }

// hookIsExecutable reports whether git would run this file. Off Windows that
// is the executable bit. Windows has no such bit — Go reports 0666 or 0444
// for every file — so any regular file in the hooks dir counts, which is
// also the truth about how git for Windows runs hooks: it hands the file to
// sh regardless of its mode.
func hookIsExecutable(fi os.FileInfo) bool {
	if !fi.Mode().IsRegular() {
		return false
	}
	if hookGOOSFn() == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// firstCommentLine quotes the file's first `#` comment line, skipping the
// shebang — `#!/bin/sh` is on every hook ever written and identifies none of
// them. "no comment line" when there is none, said in words: an empty pair
// of quotes in the middle of a report reads like a bug in the report.
func firstCommentLine(data []byte) string {
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#!") || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(trimmed) > foreignHookCommentLimit {
			trimmed = trimmed[:foreignHookCommentLimit] + "…"
		}
		return fmt.Sprintf("%q", trimmed)
	}
	return "no comment line"
}
