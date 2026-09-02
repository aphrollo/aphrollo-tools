package tdd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// A repo with a workspace manifest and no laws gets the suites and nothing
// else: every rule about its source text still lives in prose somebody has to
// remember. That is the state the law engine exists to end, and the one moment
// to say so is session start — one line, once, never a block. A repo that HAS
// laws is told nothing, because it already knows.
func ratchetHintLine(cwd string) string {
	repo := RepoRoot(cwd)
	if repo == "" || !hasWorkspaceManifest(repo) || hasAnyLaw(repo) {
		return ""
	}
	return fmt.Sprintf(
		"aphrollo: no ratchet laws in %s — the gate runs suites only; see aphrollo README %q",
		repo, "Ratchet laws")
}

// hasWorkspaceManifest reports whether repo is a project this gate governs at
// all. A cargo workspace manifest is the one it can say something useful
// about: the laws' scope globs, the always-run list and the baseline globs are
// all written against that layout.
func hasWorkspaceManifest(repo string) bool {
	info, err := os.Stat(filepath.Join(repo, "Cargo.toml"))
	return err == nil && !info.IsDir()
}

// hasAnyLaw reports whether the repo declares at least one law. An EMPTY
// `.ratchet/laws/` is the same as none: a directory nobody put a rule in
// enforces nothing, and treating its existence as an answer would silence the
// hint for the repo that most needs it.
func hasAnyLaw(repo string) bool {
	return ratchet.HasLaws(repo)
}
