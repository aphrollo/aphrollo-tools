//go:build unix

package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file the hook cannot write stays as it is, and the line does not claim
// otherwise. Unix only: a read-only mode bit does not stop the owner writing
// on Windows.
func TestPostEdit_GofmtUnwritableFileIsNotClaimedFormatted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, unformattedGo)
	if err := os.Chmod(src, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o644) })

	got := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))

	if strings.Contains(got, "gofmt formatted") {
		t.Fatalf("the write failed, yet the line claims a format:\n%s", got)
	}
	if body := readString(t, src); body != unformattedGo {
		t.Fatalf("an unwritable file changed: %q", body)
	}
}
