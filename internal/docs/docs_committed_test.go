package docs

import "testing"

// `docs check` runs the SAME rule as the doc_reference_exists law and had the
// same hole in it (borld#301): it resolved a citation against whatever sat on
// the author's disk, so a file .gitignore kept out of the commit satisfied a
// citation no other checkout could follow. The law's fix has to reach this
// path too — it is the one a repo with no `.ratchet/` of its own relies on.

// TestCheckFiles_ACitationToAFileNoCommitCarriesIsAMiss is the escape, run
// through the CLI half: docs/notes.md is on disk and in no commit.
func TestCheckFiles_ACitationToAFileNoCommitCarriesIsAMiss(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "docs/notes.md", "x")
	writeRepoFile(t, root, "docs/guide.md", "see `docs/notes.md` for the rest\n")
	got, err := CheckFiles(root, []string{"docs/guide.md"}, []string{"docs/guide.md"})
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	if len(got) != 1 || got[0].Ref != "docs/notes.md" {
		t.Fatalf("findings = %+v, want one miss for docs/notes.md — no commit carries it", got)
	}
}

// TestCheckFiles_ACitationToACommittedFileIsClean is the other half: what the
// commit does carry still resolves, or the guard bans citing anything.
func TestCheckFiles_ACitationToACommittedFileIsClean(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "docs/notes.md", "x")
	writeRepoFile(t, root, "docs/guide.md", "see `docs/notes.md` for the rest\n")
	got, err := CheckFiles(root, []string{"docs/guide.md"}, []string{"docs/guide.md", "docs/notes.md"})
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("findings = %+v, want none — docs/notes.md is in the commit", got)
	}
}
