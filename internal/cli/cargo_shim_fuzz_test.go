package cli

import (
	"strings"
	"testing"
)

// FuzzCargoShimArgv feeds an arbitrary argv (same "\x1f"-joined-blob shape as
// FuzzGitShimArgv) to the cargo shim's pure argv classifiers. These decide
// whether a cargo invocation takes a build slot at all, and cargoRunArgsTo-
// BuildArgs REWRITES argv into a `cargo build` call — a crafted `cargo run`
// invocation (a bare "--", a "-V" mid-flag-cluster, an empty token) must
// classify and rewrite cleanly rather than panic ahead of ever touching a
// lock or a real cargo.
func FuzzCargoShimArgv(f *testing.F) {
	seeds := []string{
		"build",
		"run\x1f--bin\x1fx",
		"run\x1f--\x1f--version",
		"test\x1f--\x1f--nocapture",
		"--version",
		"-V",
		"nextest\x1frun",
		"mutants",
		"bench",
		"metadata\x1f--format-version\x1f1",
		"",
		"--",
		"run\x1f--\x1f",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, blob string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("cargo shim argv classifiers panicked on argv=%q: %v", argvFromBlob(blob), r)
			}
		}()
		args := argvFromBlob(blob)
		_ = cargoVerb(args)
		_ = isCargoLongVerb(args)
		_ = isCargoReadOnlyVerb(args)
		_ = cargoPrewarmArgs(args)

		rewritten := cargoRunArgsToBuildArgs(args)

		// No program argument (anything AT OR AFTER the first bare "--") may
		// leak into the rewritten `cargo build` argv: cargo build has no
		// launched program to hand them to, and a leaked "--nocapture" or
		// similar would silently become a build flag. Every token BEFORE the
		// "--" maps 1:1 into the output (itself, or "build" in place of the
		// verb), so the exact output length pins it — a stray extra element
		// can only have come from past the cut.
		ddIdx := -1
		for i, a := range args {
			if a == "--" {
				ddIdx = i
				break
			}
		}
		want := len(args)
		if ddIdx >= 0 {
			want = ddIdx
		}
		if len(rewritten) != want {
			t.Fatalf("cargoRunArgsToBuildArgs(%q) = %q (len %d) — want length %d (everything at/after the first \"--\" dropped)", args, rewritten, len(rewritten), want)
		}

		// A `cargo run` invocation's rewrite must actually invoke `build`:
		// the first non-flag token in the result is the verb.
		if isCargoRunVerb(args) {
			verb := ""
			for _, a := range rewritten {
				if !strings.HasPrefix(a, "-") {
					verb = a
					break
				}
			}
			if verb != "build" {
				t.Fatalf("cargoRunArgsToBuildArgs(%q) = %q — a run verb must rewrite to build, first non-flag token was %q", args, rewritten, verb)
			}
		}
	})
}
