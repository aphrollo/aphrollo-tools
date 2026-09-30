package precommit

import (
	"strings"
	"testing"
)

// TestNewSuppression_AllowsAppendingAReasonToAnExistingNoqa: the ratchet law
// asks a noqa for a reason, and the edit that adds one changes a line that
// already carried the suppression, so the commit introduces nothing.
func TestNewSuppression_AllowsAppendingAReasonToAnExistingNoqa(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "svc.py", "def f():\n    try:\n        pass\n    except Exception as e:  # noqa: BLE001\n        pass\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "svc")

	write(t, root, "svc.py", "def f():\n    try:\n        pass\n    except Exception as e:  # noqa: BLE001  one failed heal must not stop the rest\n        pass\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("documenting an existing noqa must not block, got %q", msg)
	}
}

// TestNewSuppression_BlocksANewCodeOnAnExistingNoqa: the reason edit is free,
// a wider suppression is not.
func TestNewSuppression_BlocksANewCodeOnAnExistingNoqa(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "svc.py", "def f():\n    x = 1  # noqa: BLE001\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "svc")

	write(t, root, "svc.py", "def f():\n    x = 1  # noqa: BLE001, E501  reason\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); !strings.Contains(msg, suppressionCommitHeader) {
		t.Fatalf("a new code on an existing noqa is introduced and must block, got %q", msg)
	}
}
