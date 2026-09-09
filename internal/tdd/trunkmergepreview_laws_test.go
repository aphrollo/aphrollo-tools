package tdd

import (
	"strconv"
	"strings"
	"testing"
)

// bigFileLines builds a Go file of n lines: a package clause, then filler
// comments, so a law counting LINES has something to count and `go vet` still
// reads it as valid Go.
func bigFileLines(pkg string, head, tail int) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n")
	for i := range head {
		b.WriteString("// head " + string(rune('a'+i)) + "\n")
	}
	b.WriteString("\nfunc Middle() {}\n\n")
	for i := range tail {
		b.WriteString("// tail " + string(rune('a'+i)) + "\n")
	}
	return b.String()
}

// sizeLawTree writes a size law into an already-initialised repo: a file may
// not pass `max` lines, with an empty baseline so nothing is grandfathered.
func writeSizeLaw(t *testing.T, root string, max int) {
	t.Helper()
	write(t, root, ".ratchet/laws/module-size.toml", `
name = "module-size"
description = "A file may not grow past the ceiling this law names"
severity = "deny"
baseline = ".ratchet/baselines/module-size.txt"

[scope]
include = ["pkg/**/*.go"]

[matcher]
kind = "line-count"
max  = `+strconv.Itoa(max)+`
`)
	write(t, root, ".ratchet/baselines/module-size.txt", "")
}

// The escape this pins is #596: PR #595 grew internal/tdd/main_test.go's
// TestMain and a concurrent trunk commit grew the same function, each side
// clean under test_main_exit on its own, and the MERGED tree pushed m.Run()
// past the law's window — red on main from the moment it landed, with the
// local gate never having judged the combination. trunkMergePreviewStage
// already builds exactly that combination and vetted it; a law is not a
// compiler error, so nothing looked.
func TestTrunkMergePreview_BlocksALawOnlyTheMergedTreeBreaks(t *testing.T) {
	root := makeGoRepo(t)
	trunk := trunkBranch(root)
	if trunk == "" {
		t.Fatal("setup: could not resolve a trunk branch for the fixture repo")
	}

	writeSizeLaw(t, root, 15)
	write(t, root, "pkg/big.go", bigFileLines("p", 4, 4))
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "the law and the file it judges")

	gitDo(t, root, "checkout", "-q", "-b", "lane")

	// Trunk grows the tail to the ceiling exactly: 15 lines, still clean.
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "pkg/big.go", bigFileLines("p", 4, 7))
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "grow the tail")

	// The lane grows the head by as much: also under the ceiling on its own.
	gitDo(t, root, "checkout", "-q", "lane")
	write(t, root, "pkg/big.go", bigFileLines("p", 7, 4))
	gitDo(t, root, "add", ".")

	res := trunkMergePreviewStage("precommit", root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("the merged tree breaks a law neither side breaks alone; the preview must refuse it, got: %+v", res)
	}
	for _, want := range []string{"module-size", "pkg/big.go", trunk} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("the refusal must name %q: %s", want, res.Message)
		}
	}
}

// The other half, and the one that decides whether this stage is usable: a
// law trunk ALREADY breaks is not the lane's doing, and refusing every commit
// in every lane until somebody else fixes main is a block no lane can clear.
func TestTrunkMergePreview_AllowsALawTrunkAlreadyBreaks(t *testing.T) {
	root := makeGoRepo(t)
	trunk := trunkBranch(root)
	if trunk == "" {
		t.Fatal("setup: could not resolve a trunk branch for the fixture repo")
	}

	writeSizeLaw(t, root, 15)
	write(t, root, "pkg/big.go", bigFileLines("p", 4, 4))
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "the law and the file it judges")

	gitDo(t, root, "checkout", "-q", "-b", "lane")

	// Trunk breaks the ceiling by itself.
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "pkg/big.go", bigFileLines("p", 4, 14))
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk goes over the ceiling")

	// The lane touches something else entirely.
	gitDo(t, root, "checkout", "-q", "lane")
	write(t, root, "pkg/small.go", "package p\n\nfunc Small() {}\n")
	gitDo(t, root, "add", ".")

	res := trunkMergePreviewStage("precommit", root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("a law trunk already breaks is not this lane's to answer for: %s", res.Message)
	}
}
