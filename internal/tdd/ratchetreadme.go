package tdd

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A repo's laws are read by whoever edits them, and the only spec for the law
// schema lived in aphrollo's own README — on the machine that installed the
// binary, at a path nothing in the consuming repo can cite. `gate init` writes
// it INTO the repo, beside the laws it describes, so a law can point at
// `.ratchet/README.md` and the citation resolves for everyone.

//go:embed ratchet_laws.md
var ratchetLawsSpec string

const ratchetReadmeHeader = `<!-- Written by ` + "`aphrollo install`" + `. Edit the aphrollo README's
ratchet-spec section, not this file: the next init overwrites it. -->

`

// RatchetReadme is the managed file's exact bytes.
func RatchetReadme() string {
	return ratchetReadmeHeader + strings.TrimSuffix(ratchetLawsSpec, "\n") + "\n"
}

// WriteRatchetReadme writes <repo>/.ratchet/README.md when the repo has laws
// (or force). It reports whether the file changed: a second run over the same
// binary is byte-identical and writes nothing.
func WriteRatchetReadme(repoRoot string, force bool) (bool, error) {
	if repoRoot == "" {
		return false, nil
	}
	dir := filepath.Join(repoRoot, ".ratchet")
	if _, err := os.Stat(dir); err != nil && !force {
		return false, nil
	}
	path := filepath.Join(dir, "README.md")
	want := RatchetReadme()
	if have, err := os.ReadFile(path); err == nil && string(have) == want {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
