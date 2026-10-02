package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// Status is what Check decides about one binary and one repo.
type Status int

const (
	// Satisfied: the repo declares no minimum, or this binary meets it.
	Satisfied Status = iota
	// TooOld: the repo declares a newer minimum than this binary.
	TooOld
	// Malformed: a declaration this binary cannot read, so it can decide nothing.
	Malformed
)

// Verdict is a Status and the one line that says it; Line is empty for
// Satisfied.
type Verdict struct {
	Status Status
	Line   string
}

// declarations are the two places a repo states the oldest binary it accepts:
// a Cargo workspace keeps the gate's keys in its manifest, any other repo in
// aphrollo.toml. Both are read, so a repo that has both cannot be read two ways.
var declarations = []struct{ file, table string }{
	{"aphrollo.toml", "[aphrollo]"},
	{"Cargo.toml", "[workspace.metadata.aphrollo]"},
}

// Check judges root against the binary version have. The stricter of two
// declarations wins; one that cannot be read beats a minimum, because a
// constraint nobody could compare says nothing about what is safe.
func Check(root string, have Version) Verdict {
	var strictest Requirement
	declared := false
	for _, d := range declarations {
		raw, ok := core.TomlStringIn(filepath.Join(root, d.file), d.table, "requires")
		if !ok {
			continue
		}
		req, err := ParseRequires(beforeComment(raw))
		if err != nil {
			return Verdict{Malformed, fmt.Sprintf("aphrollo: %s: %v", d.file, err)}
		}
		if !declared || strictest.Min.Less(req.Min) {
			strictest, declared = req, true
		}
	}
	if !declared || !have.Less(strictest.Min) {
		return Verdict{}
	}
	return Verdict{TooOld, fmt.Sprintf("aphrollo too old here: this repo requires %s, this is %s; run aphrollo update", strictest, have)}
}

// beforeComment cuts what the line reader leaves after a quoted value: it
// strips only the outer quotes, so `">=1.4" # why` arrives as `>=1.4" # why`.
func beforeComment(raw string) string {
	value, _, _ := strings.Cut(raw, `"`)
	return value
}

// RepoRoot is the nearest directory at or above dir that holds a .git entry (a
// directory, or the file a linked worktree has), or "" outside any repo. It
// walks the filesystem rather than asking git, because a hook asks this on
// every call and a process per call is the cost a version guard must not add.
func RepoRoot(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// CheckAt is Check for the repo dir sits in; outside any repo there is
// nothing to be too old for.
func CheckAt(dir string, have Version) Verdict {
	root := RepoRoot(dir)
	if root == "" {
		return Verdict{}
	}
	return Check(root, have)
}
