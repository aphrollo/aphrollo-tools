package tdd

import "testing"

// TestSessionID_FallsBackToTheNameClaudeCodeActuallySets is the break that
// made `aphrollo gate allow` unreachable: the environment carries
// CLAUDE_CODE_SESSION_ID and every reader asked for CLAUDE_SESSION_ID, so an
// override could never attach to a session that plainly existed.
func TestSessionID_FallsBackToTheNameClaudeCodeActuallySets(t *testing.T) {
	t.Setenv(sessionEnv, "")
	t.Setenv(sessionCodeEnv, "e66fa701-6877-4a74-bc98-f31b75d0cfb2")

	if got, want := SessionID(), "e66fa701-6877-4a74-bc98-f31b75d0cfb2"; got != want {
		t.Errorf("SessionID() = %q, want %q — the id Claude Code sets", got, want)
	}
}

// The short name still wins when something sets it, so an environment that
// already worked is not changed by the fallback.
func TestSessionID_PrefersTheShortNameWhenBothAreSet(t *testing.T) {
	t.Setenv(sessionEnv, "short")
	t.Setenv(sessionCodeEnv, "long")

	if got := SessionID(); got != "short" {
		t.Errorf("SessionID() = %q, want %q", got, "short")
	}
}

// Neither set is the genuine "no session" case the callers branch on.
func TestSessionID_EmptyWhenNeitherIsSet(t *testing.T) {
	t.Setenv(sessionEnv, "")
	t.Setenv(sessionCodeEnv, "")

	if got := SessionID(); got != "" {
		t.Errorf("SessionID() = %q, want empty", got)
	}
}
