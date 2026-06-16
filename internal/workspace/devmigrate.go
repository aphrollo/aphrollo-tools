package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// This file holds claim's dev-tier hardening: applying the api clone's
// migrations to the isolated dev DB before the dev-api restart, and a
// build-before-claim advisory for the web tier. Both target the same hazard —
// the dev units run as `debian` and follow the .devclaim symlink into whatever
// worktree was just claimed, so the claimed tree's state (DB schema, generated
// caches) has to be reconciled at claim time, not discovered as a 500 or an
// EACCES later.

// defaultDevDBURL mirrors APHROLLO_DB_URL in the aphrollo-dev-api unit: the
// isolated dev postgres on :5432 (NOT prod's :5433). Overridable for tests / a
// relocated dev DB via APHROLLO_DEV_DB_URL.
const defaultDevDBURL = "postgres://aphrollo:aphrollo@127.0.0.1:5432/aphrollo?sslmode=disable"

func devDBURL() string {
	if u := os.Getenv("APHROLLO_DEV_DB_URL"); u != "" {
		return u
	}
	return defaultDevDBURL
}

// gooseBin resolves the goose binary: APHROLLO_GOOSE_BIN, then $PATH, then the
// operator's go-install location. Returns "" when none is found.
func gooseBin() string {
	if b := os.Getenv("APHROLLO_GOOSE_BIN"); b != "" {
		return b
	}
	if p, err := exec.LookPath("goose"); err == nil {
		return p
	}
	const fallback = "/home/debian/go/bin/goose"
	if fileExists(fallback) {
		return fallback
	}
	return ""
}

// gooseUp runs `goose up` against the dev DB using the clone's migrations dir,
// so a freshly-claimed api worktree and the dev DB never drift (the gap that
// makes a SUBSET of endpoints 500 after a dev-api restart). It runs BEFORE the
// restart in the claim sequence, so the api boots against the migrated schema.
// Forward-only against the isolated dev pg; it never touches prod (:5433).
func gooseUp(clone string, stdout, stderr io.Writer) error {
	migDir := filepath.Join(clone, "migrations")
	if !dirExists(migDir) {
		fmt.Fprintf(stdout, "  no migrations/ in %s — nothing to apply\n", clone)
		return nil
	}
	bin := gooseBin()
	if bin == "" {
		return fmt.Errorf("goose not found (set APHROLLO_GOOSE_BIN or add goose to PATH)")
	}
	cmd := exec.Command(bin, "-dir", migDir, "postgres", devDBURL(), "up")
	cmd.Dir = clone
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("goose up (dev db): %w", err)
	}
	return nil
}

// buildFirstNote returns a build-before-claim advisory for the web tier, or ""
// when it doesn't apply. The dev-rlndx unit runs as `debian` and generates
// .svelte-kit/.vite/paraglide on first serve; if the coder hasn't built/tested
// the worktree first, those land debian-owned and EACCES the coder's tooling.
// We nudge only when the worktree looks unbuilt (no .svelte-kit yet), so a tree
// that was already tested isn't nagged. The UMask=0002 dev-unit change removes
// the underlying hazard; this is the tool-side guardrail until it deploys.
func buildFirstNote(svc, wt string) string {
	if svc != "rlndx" {
		return ""
	}
	for _, p := range []string{
		filepath.Join(wt, "apps", "rlndx", ".svelte-kit"),
		filepath.Join(wt, ".svelte-kit"),
	} {
		if dirExists(p) {
			return "" // already built — no poisoning risk
		}
	}
	return "build/test/lint this worktree as coder BEFORE claiming — the dev server runs " +
		"as debian and will generate caches (.svelte-kit/.vite/paraglide); a claimed worktree " +
		"is view-only. (Harmless once the dev-tier UMask=0002 change deploys.)"
}
