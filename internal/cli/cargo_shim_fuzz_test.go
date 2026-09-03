package cli

import "testing"

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
		_ = isCargoRunVerb(args)
		_ = isCargoReadOnlyVerb(args)
		_ = cargoPrewarmArgs(args)
		_ = cargoRunArgsToBuildArgs(args)
	})
}
