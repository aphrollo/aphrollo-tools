package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shimDir = "C:/Users/olive/bin/cargo-queue"

func TestClaudeMDBlockCarriesTheOperatingInstructions(t *testing.T) {
	block := ClaudeMDBlock(shimDir, false)
	for _, want := range []string{
		claudeMDBegin, claudeMDEnd, shimDir,
		"gate:", "QUEUED-SKIPPED", "cargo check -p", ".ratchet/laws",
		"aphrollo ratchet", "aphrollo gate gc", "aphrollo gate stats",
		"aphrollo gate init",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not mention %q", want)
		}
	}
	if n := strings.Count(block, "\n"); n < 15 || n > 40 {
		t.Errorf("block is %d lines — it has to be readable in one glance", n)
	}
	if strings.Contains(block, "commit-msg") {
		t.Error("a workspace that did not ask for the undercover rule must not be told it")
	}
	if !strings.Contains(ClaudeMDBlock(shimDir, true), "commit-msg") {
		t.Error("a workspace with undercover = true must get the commit-message rule")
	}
}

func TestPatchClaudeMDAppendsOnceAndIsIdempotent(t *testing.T) {
	block := ClaudeMDBlock(shimDir, false)
	first, changed := PatchClaudeMD([]byte("# Project\n\nSome guidance.\n"), block)
	if !changed {
		t.Fatal("a file with no block must gain one")
	}
	if !strings.HasSuffix(string(first), block) {
		t.Errorf("the block must land at the END:\n%s", first)
	}

	second, changed := PatchClaudeMD(first, block)
	if changed {
		t.Error("a second run must change nothing")
	}
	if string(second) != string(first) {
		t.Error("a second run must be byte-identical")
	}
	if strings.Count(string(second), claudeMDBegin) != 1 {
		t.Errorf("the block was duplicated:\n%s", second)
	}
}

// A block already present is replaced where it sits: someone may have put it
// somewhere deliberate, and moving it to the end on every init would churn the
// file forever.
func TestPatchClaudeMDReplacesAnExistingBlockInPlace(t *testing.T) {
	stale := "# Project\n\n" + claudeMDBegin + "\nold text nobody updated\n" + claudeMDEnd + "\n\n## Conventions\n\nkeep me\n"
	block := ClaudeMDBlock(shimDir, false)

	out, changed := PatchClaudeMD([]byte(stale), block)
	got := string(out)
	if !changed {
		t.Fatal("a stale block must be replaced")
	}
	if strings.Contains(got, "old text nobody updated") {
		t.Errorf("the stale block survived:\n%s", got)
	}
	if strings.Count(got, claudeMDBegin) != 1 || strings.Count(got, claudeMDEnd) != 1 {
		t.Errorf("markers were duplicated:\n%s", got)
	}
	if !strings.Contains(got, "## Conventions") || !strings.Contains(got, "keep me") {
		t.Errorf("content after the block was lost:\n%s", got)
	}
	if strings.Index(got, claudeMDBegin) > strings.Index(got, "## Conventions") {
		t.Errorf("the block moved instead of being replaced in place:\n%s", got)
	}
}

// A file hand-edited mid-block leaves one marker behind. Nesting a fresh block
// inside a half-open one would make every later init unparseable.
func TestPatchClaudeMDRecoversFromAnOrphanMarker(t *testing.T) {
	block := ClaudeMDBlock(shimDir, false)
	out, _ := PatchClaudeMD([]byte("# Project\n\n"+claudeMDBegin+"\nhalf a block\n"), block)
	got := string(out)
	if strings.Count(got, claudeMDBegin) != 1 || strings.Count(got, claudeMDEnd) != 1 {
		t.Fatalf("markers are not balanced:\n%s", got)
	}
	if !strings.HasSuffix(got, block) {
		t.Errorf("the recovered file must end with one whole block:\n%s", got)
	}
}

func TestPatchClaudeMDPreservesCRLF(t *testing.T) {
	block := ClaudeMDBlock(shimDir, false)
	out, _ := PatchClaudeMD([]byte("# Project\r\n\r\nGuidance.\r\n"), block)
	if strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Error("a CRLF file must stay CRLF throughout")
	}
	again, changed := PatchClaudeMD(out, block)
	if changed || string(again) != string(out) {
		t.Error("a CRLF file must be byte-identical on a second run")
	}
}

func TestWriteClaudeMDIsANoOpWithoutTheFileUnlessForced(t *testing.T) {
	repo := t.TempDir()
	changed, err := WriteClaudeMD(repo, shimDir, false)
	if err != nil || changed {
		t.Fatalf("a repo with no CLAUDE.md must be left alone (changed=%v err=%v)", changed, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("no file must be invented")
	}

	changed, err = WriteClaudeMD(repo, shimDir, true)
	if err != nil || !changed {
		t.Fatalf("--claude-md must create the file (changed=%v err=%v)", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
	if err != nil || !strings.HasPrefix(string(data), claudeMDBegin) {
		t.Fatalf("created file = %q (%v)", data, err)
	}
}

// Run twice, byte-identical: the file is a source file in the consuming repo,
// and a block that churned would show up as a diff on every session start.
func TestWriteClaudeMDIsByteIdenticalOnASecondRun(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "CLAUDE.md")
	mustWrite(t, path, "# Borld\n\nProject guide.\n")
	mustWrite(t, filepath.Join(repo, "Cargo.toml"),
		"[workspace]\n[workspace.metadata.aphrollo]\nundercover = true\n")

	if changed, err := WriteClaudeMD(repo, shimDir, false); err != nil || !changed {
		t.Fatalf("first run: changed=%v err=%v", changed, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "commit-msg") {
		t.Error("the workspace's undercover flag must reach the block")
	}

	if changed, err := WriteClaudeMD(repo, shimDir, false); err != nil || changed {
		t.Fatalf("second run: changed=%v err=%v — nothing moved, so nothing should be written", changed, err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Error("the second run rewrote the file")
	}
	if !strings.Contains(string(second), "Project guide.") {
		t.Error("the repo's own guidance was lost")
	}
}
