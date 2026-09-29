package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// replacePayload is an Edit of old to new in the file at path.
func replacePayload(t *testing.T, path, old, new string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  "Edit",
		"tool_input": map[string]string{"file_path": path, "old_string": old, "new_string": new},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDecidePreEdit_AppendingAReasonToAnExistingNoqaDoesNotWarn: the edit
// rewrites a line that already carried the suppression, so it adds none; a
// new code on it is still warned about.
func TestDecidePreEdit_AppendingAReasonToAnExistingNoqaDoesNotWarn(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "svc.py")
	line := "    except Exception as e:  # noqa: BLE001"
	if err := os.WriteFile(path, []byte("try:\n    pass\n"+line+"\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := decide(t, replacePayload(t, path, line, line+"  one heal must not stop the rest")).Action; got != Allow {
		t.Errorf("documenting an existing noqa: got %v, want Allow", got)
	}
	if got := decide(t, replacePayload(t, path, line, line+", E501")).Action; got != Warn {
		t.Errorf("a new code on an existing noqa: got %v, want Warn", got)
	}
}
