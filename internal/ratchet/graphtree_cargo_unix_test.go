//go:build !windows

package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// cargoOnPath puts a `cargo` on PATH that prints metadataDoc and appends its
// argv and CARGO_TARGET_DIR to the returned log, with $CARGO unset.
func cargoOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "cargo.log")
	doc := filepath.Join(dir, "metadata.json")
	write(t, doc, metadataDoc)
	script := "#!/bin/sh\nprintf 'args=%s target=%s\\n' \"$*\" \"$CARGO_TARGET_DIR\" >> '" + log + "'\ncat '" + doc + "'\n"
	if err := proc.WriteExecutable(filepath.Join(dir, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO", "")
	t.Setenv("CARGO_TARGET_DIR", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// TestCargoMetadata_RunsCargoFromThePathAsTheTreeSays proves the query runs
// the `cargo` on PATH when $CARGO is unset, over the tree's own manifest,
// with --offline and CARGO_TARGET_DIR only when the law carries them.
func TestCargoMetadata_RunsCargoFromThePathAsTheTreeSays(t *testing.T) {
	log := cargoOnPath(t)
	root := t.TempDir()
	law := depGraphLaw(t, root)
	if _, err := depGraphHits(root, law); err != nil {
		t.Fatalf("depGraphHits: %v", err)
	}
	law.CargoOffline, law.CargoTargetDir = true, filepath.Join(root, "t")
	if _, err := depGraphHits(root, law); err != nil {
		t.Fatalf("depGraphHits: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("cargo on PATH never ran: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	manifest := filepath.Join(root, "Cargo.toml")
	want := []string{
		"args=metadata --format-version 1 --manifest-path " + manifest + " target=",
		"args=metadata --format-version 1 --manifest-path " + manifest + " --offline target=" + filepath.Join(root, "t"),
	}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("cargo calls =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
