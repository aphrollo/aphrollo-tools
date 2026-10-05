package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pins the CONTRACT the style block must state (terse, findings-first,
// verbatim numbers/code, the security-warning carve-out), not its exact
// wording, and its size: ~80 tokens is the budget, so a generous line-count
// ceiling catches a block that grew past the point of being cheap to repeat
// on every prompt.
func TestStyleBlock_StatesItsContractAndStaysShort(t *testing.T) {
	body := StyleBlock()
	for _, want := range []string{"terse", "Findings first", "never drop not/never/only", "security"} {
		if !strings.Contains(body, want) {
			t.Errorf("style block missing %q:\n%s", want, body)
		}
	}
	if n := strings.Count(body, "\n") + 1; n > 12 {
		t.Errorf("style block is %d lines, want <= 12", n)
	}
}

// No override and no env var: the default is terse.
func TestReplyStyleFor_DefaultsTerseWithoutOverrideOrEnv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if got := replyStyleFor("sess-default"); got != "terse" {
		t.Fatalf("replyStyleFor = %q, want terse", got)
	}
}

// APHROLLO_REPLY_STYLE=plain sets the machine-wide default when a session has
// not overridden it.
func TestReplyStyleFor_EnvSetsMachineDefault(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(ReplyStyleEnvVar, "plain")
	if got := replyStyleFor("sess-env"); got != "plain" {
		t.Fatalf("replyStyleFor = %q, want plain", got)
	}
}

// A session's own `/tdd style` override wins over the machine's env default.
func TestReplyStyleFor_SessionOverrideWinsOverEnv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(ReplyStyleEnvVar, "plain")
	const sess = "sess-override"
	if err := setReplyStyle(sess, "terse"); err != nil {
		t.Fatal(err)
	}
	if got := replyStyleFor(sess); got != "terse" {
		t.Fatalf("replyStyleFor = %q, want terse (session override over env default)", got)
	}
}

// reply_style in the user's config sets the machine-wide default, as the
// deprecated variable did, and a session's own override still wins over it.
func TestReplyStyleFor_TheUsersConfigSetsTheMachineDefault(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(ReplyStyleEnvVar, "")
	dir := t.TempDir()
	t.Setenv("TRELLIS_CONFIG", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("reply_style = \"plain\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := replyStyleFor("sess-config"); got != "plain" {
		t.Fatalf("replyStyleFor = %q, want plain", got)
	}
	if err := setReplyStyle("sess-config", "terse"); err != nil {
		t.Fatal(err)
	}
	if got := replyStyleFor("sess-config"); got != "terse" {
		t.Fatalf("a session override must win over the config, got %q", got)
	}
}
