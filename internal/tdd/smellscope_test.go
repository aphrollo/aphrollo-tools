package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// skippingTestFile writes a test file that legitimately skips — a platform
// guard, of which this repo alone has fourteen — and returns its path.
func skippingTestFile(t *testing.T) (path, content string) {
	t.Helper()
	content = "package m\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n" +
		"\tif runtime.GOOS != \"linux\" {\n\t\tt.Skip(\"linux only\")\n\t}\n\tuse(1)\n}\n"
	path = filepath.Join(t.TempDir(), "guard_test.go")
	mustWrite(t, path, content)
	return path, content
}

// The smells judged the whole file, so every later edit to a file that
// legitimately skips was denied — and the fix for a denial that has no fix is
// to stop working in that file.
func TestPreEdit_EditElsewhereInAFileThatAlreadySkips(t *testing.T) {
	path, _ := skippingTestFile(t)
	raw := ratchetPayload(t, "Edit", path, map[string]any{
		"old_string": "use(1)",
		"new_string": "use(2)",
	})

	if d := decide(t, string(raw)); d.Action == Block {
		t.Fatalf("an edit that adds no smell must flow: %s", d.Reason)
	}
}

func TestPreEdit_RewriteThatKeepsAnExistingSkip(t *testing.T) {
	path, content := skippingTestFile(t)
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": content + "\nfunc TestTwo(t *testing.T) { use(3) }\n",
	})

	if d := decide(t, string(raw)); d.Action == Block {
		t.Fatalf("rewriting a file around an existing skip must flow: %s", d.Reason)
	}
}

// The rule itself is unchanged: a skip this edit INTRODUCES is still denied.
func TestPreEdit_ANewlyAddedSkipIsStillDenied(t *testing.T) {
	path, content := skippingTestFile(t)
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": content + "\nfunc TestTwo(t *testing.T) { t.Skip(\"later\") }\n",
	})

	if d := decide(t, string(raw)); d.Action != Block {
		t.Fatal("a newly added skip must still be denied")
	}
}

// A platform guard is a real thing, so the rule needs a way to say yes that
// leaves a record — a denial with no escape is answered by deleting the test.
func TestPreEdit_AnEscapedSkipIsAdmittedAndRecorded(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	path, content := skippingTestFile(t)
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": content + "\nfunc TestTwo(t *testing.T) {\n\tt.Skip(\"no docker here\") // skip-ok: docker is not installed on the CI image\n}\n",
	})

	d := decide(t, string(raw))
	if d.Action == Block {
		t.Fatalf("an explained skip must flow: %s", d.Reason)
	}
	LogEditDecision(raw, d)
	requireLoggedVerdict(t, cfg, "smell-escape:disabled-test")
}

// The escape may sit on the line ABOVE, which is where a longer reason goes.
func TestPreEdit_AnEscapedSleepIsAdmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing_test.go")
	mustWrite(t, path, "package m\n")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "package m\n\nfunc TestWait(t *testing.T) {\n" +
			"\t// real-time: the OS file lock has no observable hand-off event\n\ttime.Sleep(20 * time.Millisecond)\n}\n",
	})

	if d := decide(t, string(raw)); d.Action == Block {
		t.Fatalf("an explained sleep must flow: %s", d.Reason)
	}
}

// Each escape admits its OWN kind. A sleep's reason is not a licence to skip.
func TestPreEdit_AnEscapeAdmitsOnlyItsOwnKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrong_test.go")
	mustWrite(t, path, "package m\n")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "package m\n\nfunc TestOne(t *testing.T) {\n\tt.Skip(\"later\") // real-time: unrelated\n}\n",
	})

	d := decide(t, string(raw))
	if d.Action != Block {
		t.Fatal("a skip explained as a real-time sleep is not explained at all")
	}
	if !strings.Contains(d.Reason, "skipped") {
		t.Fatalf("the denial must still name the skip, got: %q", d.Reason)
	}
}
