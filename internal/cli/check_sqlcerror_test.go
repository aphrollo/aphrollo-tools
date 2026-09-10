package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A repo whose sqlc config the parser cannot read is NOT a repo without an
// sqlc config: `check` has no idea whether that config's generated code has
// drifted. Reporting it as "[skip] no sqlc config" states the opposite of
// what is known and passes the guard; the sibling `sqlc check` verb exits 1
// with the reason on the identical call.
func TestCheckSqlc_MalformedConfigIsAMissNamingIt_NotASkip(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	// Discovered by name, rejected by the parser: no sql entries.
	writeFile(t, filepath.Join(root, "sqlc.yaml"), "version: \"2\"\n")
	gitCommitAll(t, root, "seed")

	var out, errb bytes.Buffer
	ok := checkSqlc(root, &out, &errb)
	if ok {
		t.Fatalf("an unreadable sqlc config must fail the guard; stdout:\n%s", out.String())
	}
	got := out.String()
	if strings.Contains(got, "[skip] no sqlc config") {
		t.Fatalf("a config that exists but cannot be parsed must not be reported as an absent one; got:\n%s", got)
	}
	if !strings.Contains(got, "sqlc.yaml") {
		t.Fatalf("the miss must name the config that could not be read; got:\n%s", got)
	}
}

// The skip is still correct for the case it was written for: a repo that
// declares no sqlc config at all is not gated by sqlc, and that is not a
// finding.
func TestCheckSqlc_NoConfigStillSkips(t *testing.T) {
	root := t.TempDir()

	var out, errb bytes.Buffer
	if !checkSqlc(root, &out, &errb) {
		t.Fatalf("a repo with no sqlc config must pass the guard; stdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "check: sqlc → [skip] no sqlc config") {
		t.Fatalf("stdout missing the no-config skip line, got:\n%s", out.String())
	}
}
