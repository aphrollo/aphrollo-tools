package git

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// blobsOf commits the file `big.txt` in versions that share most of their
// lines, so a repack stores the later ones as deltas of the earlier, and
// answers the object name and content of every version.
func blobsOf(t *testing.T, dir string) (oids []string, texts []string) {
	t.Helper()
	var lines []string
	for i := range 400 {
		lines = append(lines, fmt.Sprintf("line %03d of the shared body, long enough to be worth a delta", i))
	}
	for v := range 6 {
		lines[v*50] = fmt.Sprintf("version %d changes this line", v)
		text := strings.Join(lines, "\n") + "\n"
		write(t, dir, "big.txt", text)
		gitT(t, dir, "add", "big.txt")
		gitT(t, dir, "commit", "-q", "-m", fmt.Sprintf("v%d", v))
		oids = append(oids, gitT(t, dir, "rev-parse", "HEAD:big.txt"))
		texts = append(texts, text)
	}
	return oids, texts
}

func TestReadBlob_ReadsLooseAndPackedObjectsWithoutSpawningGit(t *testing.T) {
	tests := []struct {
		name   string
		repack []string
	}{
		{"loose objects", nil},
		{"offset deltas", []string{"repack", "-q", "-a", "-d", "-f", "--depth=20", "--window=20"}},
		{"reference deltas", []string{"-c", "repack.useDeltaBaseOffset=false", "repack", "-q", "-a", "-d", "-f", "--depth=20", "--window=20"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gitT(t, dir, "init", "-q", "-b", "main")
			oids, texts := blobsOf(t, dir)
			if tc.repack != nil {
				gitT(t, dir, tc.repack...)
				gitT(t, dir, "prune-packed")
			}
			c := mustNew(t, dir)

			for i, oid := range oids {
				got, err := c.ReadBlob(oid)
				if err != nil {
					t.Fatalf("version %d: %v", i, err)
				}
				if !bytes.Equal(got, []byte(texts[i])) {
					t.Errorf("version %d: ReadBlob differs from the committed text (%d bytes, want %d)", i, len(got), len(texts[i]))
				}
			}
			if n := c.Spawns(); n != 0 {
				t.Errorf("reading %d blobs spawned git %d times, want 0", len(oids), n)
			}
		})
	}
}

func TestReadBlob_ReadsAnObjectOfALinkedWorktreeFromTheCommonStore(t *testing.T) {
	main := repoWithCommit(t)
	lane := t.TempDir() + "/lane"
	gitT(t, main, "worktree", "add", "-q", "-b", "lane/x", lane)
	oid := gitT(t, main, "rev-parse", "HEAD:a.txt")
	c := mustNew(t, lane)

	got, err := c.ReadBlob(oid)

	if err != nil || string(got) != "a\n" {
		t.Fatalf("ReadBlob = %q, %v; want \"a\\n\"", got, err)
	}
	if c.Spawns() != 0 {
		t.Errorf("spawned git %d times, want 0", c.Spawns())
	}
}

func TestReadBlob_AsksGitForWhatTheFilesDoNotHold(t *testing.T) {
	dir := repoWithCommit(t)
	oid := gitT(t, dir, "rev-parse", "HEAD:a.txt")
	c := mustNew(t, dir)

	if _, err := c.ReadBlob("0123456789012345678901234567890123456789"); !errors.Is(err, ErrNoObject) {
		t.Errorf("an object nobody has: err = %v, want ErrNoObject", err)
	}
	if c.Spawns() != 1 {
		t.Errorf("an object the files lack cost %d spawns, want the one that asked git", c.Spawns())
	}
	// A name this reader does not take on at all goes to git, which reads it.
	got, err := c.ReadBlob("HEAD:a.txt")
	if err != nil || string(got) != "a\n" {
		t.Errorf("ReadBlob(HEAD:a.txt) = %q, %v; want git's answer \"a\\n\"", got, err)
	}
	if got, err := c.ReadBlob(oid); err != nil || string(got) != "a\n" {
		t.Errorf("ReadBlob(%s) = %q, %v", oid, got, err)
	}
}

func TestReadBlob_RefusesAnObjectThatIsNotABlob(t *testing.T) {
	dir := repoWithCommit(t)
	tree := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	c := mustNew(t, dir)

	if got, err := c.ReadBlob(tree); err == nil {
		t.Errorf("ReadBlob of a tree = %q, want an error", got)
	}
}

func TestApplyDelta_RefusesADeltaThatDoesNotFitItsBase(t *testing.T) {
	base := []byte("hello")
	tests := []struct {
		name  string
		delta []byte
	}{
		{"base size that is not the base's", []byte{4, 5, 5, 'a', 'b', 'c', 'd', 'e'}},
		{"copy past the end of the base", []byte{5, 3, 0x91, 4, 3}},
		{"insert longer than what is left", []byte{5, 3, 5, 'a'}},
		{"result size that is not what the instructions make", []byte{5, 9, 3, 'a', 'b', 'c'}},
		{"no result size", []byte{5}},
		{"instruction zero", []byte{5, 1, 0}},
	}
	for _, tc := range tests {
		if got, ok := applyDelta(base, tc.delta); ok {
			t.Errorf("%s: applied to %q", tc.name, got)
		}
	}
	got, ok := applyDelta(base, []byte{5, 6, 0x90, 3, 3, 'x', 'y', 'z'})
	if !ok || string(got) != "helxyz" {
		t.Errorf("a copy of three bytes then an insert of three = %q, %v; want \"helxyz\"", got, ok)
	}
}
