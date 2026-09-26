package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGhReadyScript writes a `gh` script that answers three shapes: `pr
// ready -- <branch>` (native path, exit readyExit with readyOut on stdout
// when it fails — the GraphQL-blocked marker text lives there), the
// list-pulls REST call (answers prNumber), and the sandbox-only
// ready_for_review REST fallback (exits sandboxExit).
func fakeGhReadyScript(t *testing.T, readyExit int, readyOut string, prNumber string, sandboxExit int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		ghEndpointArg +
		"case \"$1 $2\" in\n" +
		"  \"pr ready\") printf '%s' '" + readyOut + "' 1>&2; exit " + itoa(readyExit) + " ;;\n" +
		"esac\n" +
		"case \"$1 $path\" in\n" +
		"  \"api repos/acme/widgets/pulls\") printf '%s' '" + prNumber + "'; exit 0 ;;\n" +
		"  *) case \"$path\" in\n" +
		"       *ready_for_review*) exit " + itoa(sandboxExit) + " ;;\n" +
		"       *) exit 1 ;;\n" +
		"     esac ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// ghEndpointArg sets $path to the first repos/... argument wherever it sits,
// so a --method flag before the endpoint does not hide it from a fake.
const ghEndpointArg = "path=\nfor a in \"$@\"; do case \"$a\" in repos/*) path=\"$a\"; break ;; esac; done\n"

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return "1"
}

// TestGhReadyPR_NativeSucceeds proves the portable path (`gh pr ready`, which
// works via GraphQL on ordinary GitHub) is used first and, on success, the
// sandbox-only REST fallback is never touched.
func TestGhReadyPR_NativeSucceeds(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhReadyScript(t, 0, "", "", 1) // sandboxExit=1: must never be reached
	if err := ghReadyPR(repo, "feat/x"); err != nil {
		t.Fatalf("ghReadyPR: %v", err)
	}
}

// TestGhReadyPR_FallsBackWhenGraphQLIsBlocked proves the sandbox-only REST
// fallback (POST .../pulls/{n}/ccr/ready_for_review) is used ONLY when the
// native call fails with the specific GraphQL-blocked signature.
func TestGhReadyPR_FallsBackWhenGraphQLIsBlocked(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhReadyScript(t, 1, "HTTP 403: GitHub GraphQL is not available from Claude Code sessions; use the REST API", "42", 0)
	if err := ghReadyPR(repo, "feat/x"); err != nil {
		t.Fatalf("ghReadyPR: %v, want the sandbox REST fallback to succeed", err)
	}
}

// TestGhReadyPR_OtherFailuresAreNotMaskedByTheFallback proves an ordinary gh
// failure (not the GraphQL-blocked shape) propagates as-is — the fallback
// must never mask a real error (auth, network, no such PR) as an unrelated
// sandbox-endpoint failure.
func TestGhReadyPR_OtherFailuresAreNotMaskedByTheFallback(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhReadyScript(t, 1, "gh: authentication required", "42", 0)
	err := ghReadyPR(repo, "feat/x")
	if err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("ghReadyPR = %v, want the native error propagated, not the fallback's", err)
	}
}

// fakeGhEditScript writes a `gh` script answering the list-pulls REST call
// (prNumber) and the PATCH-body REST call (patchExit).
func fakeGhEditScript(t *testing.T, prNumber string, patchExit int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		ghEndpointArg +
		"case \"$path\" in\n" +
		"  \"repos/acme/widgets/pulls\") printf '%s' '" + prNumber + "'; exit 0 ;;\n" +
		"  repos/acme/widgets/pulls/*) exit " + itoa(patchExit) + " ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGhEditPRBody_PatchesTheRealPullsEndpoint proves ghEditPRBody now goes
// straight to REST's PATCH pulls/{n} (a real, portable GitHub endpoint —
// unlike ready-for-review, this needs no fallback).
func TestGhEditPRBody_PatchesTheRealPullsEndpoint(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhEditScript(t, "7", 0)
	if err := ghEditPRBody(repo, "feat/x", "new body"); err != nil {
		t.Fatalf("ghEditPRBody: %v", err)
	}
}

// TestGhEditPRBody_PropagatesAFailedPatch proves a failed PATCH surfaces as
// an error rather than being swallowed.
func TestGhEditPRBody_PropagatesAFailedPatch(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhEditScript(t, "7", 1)
	if err := ghEditPRBody(repo, "feat/x", "new body"); err == nil {
		t.Fatal("ghEditPRBody = nil, want an error on a failed PATCH")
	}
}
