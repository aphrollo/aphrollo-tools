package failfirst

import (
	"fmt"
	"path"
	"strings"
)

// A new test that is green on the old code pins behaviour that already
// exists, so no RED can be shown for it. The gate takes other evidence in its
// place; each route is a row here, so a new kind of evidence is data.
type evidenceRoute struct {
	// covers reports whether the route proves every one of names.
	covers func(repoRoot string, names []string) bool
	// advice is what the refusal tells a person to do to use the route.
	advice func(names []string) string
}

var greenAtHeadEvidence = []evidenceRoute{
	{covers: mutationProofCovers, advice: mutationProofAdvice},
}

// greenAtHeadProven reports whether some evidence route covers every test
// that passed at HEAD. Tests the proof run could not name are never covered.
func greenAtHeadProven(repoRoot string, names []string) bool {
	if len(names) == 0 {
		return false
	}
	for _, r := range greenAtHeadEvidence {
		if r.covers(repoRoot, names) {
			return true
		}
	}
	return false
}

// mutationProofCovers: each test has a recorded `mutants prove` kill whose
// mutated file still holds, in the index, the content that was broken.
func mutationProofCovers(repoRoot string, names []string) bool {
	proofs := PinProofs(repoRoot)
	for _, name := range names {
		proven := false
		for _, p := range proofs {
			if p.Test == name && stagedBlobOf(repoRoot, p.File) == p.Blob {
				proven = true
				break
			}
		}
		if !proven {
			return false
		}
	}
	return true
}

func stagedBlobOf(repoRoot, file string) string {
	out, err := git(repoRoot, "rev-parse", ":"+file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func mutationProofAdvice(names []string) string {
	var b strings.Builder
	b.WriteString("Or, if they pin behaviour that already exists, break the code under each one and commit as is:\n")
	if len(names) == 0 {
		names = []string{"<Test>"}
	}
	for _, n := range names {
		fmt.Fprintf(&b, "    aphrollo gate mutants prove --file <code file> --old <expr> --new <expr> --want-fail %s\n", n)
	}
	b.WriteString("A KILLED line records the proof for that test on the code as staged; edit the code after it and it no longer counts.\n")
	return b.String()
}

// dependencyManifests are the dependency manifests and lockfiles, by base name.
var dependencyManifests = []string{
	"package.json", "package-lock.json", "npm-shrinkwrap.json",
	"pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
}

// stagedManifests is the files of srcs that are a manifest or lockfile.
func stagedManifests(srcs []string) []string {
	var out []string
	for _, s := range srcs {
		base := path.Base(strings.ReplaceAll(s, "\\", "/"))
		for _, m := range dependencyManifests {
			if base == m {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// manifestAdvice says directly that a manifest or lockfile is bundled with
// tests that need no change, and gives the commands that split it off.
func manifestAdvice(manifests []string) string {
	if len(manifests) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "A package manifest or lockfile is staged with these tests: %s.\n", strings.Join(manifests, ", "))
	b.WriteString("A dependency or script change is not the implementation they test, so the tests pass without it. Commit the lockfile change on its own:\n")
	fmt.Fprintf(&b, "    git commit -m \"<the dependency change>\" -- %s\n", strings.Join(manifests, " "))
	return b.String()
}
