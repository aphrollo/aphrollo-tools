package install

import (
	_ "embed"
	"os"
	"strings"
)

// The gate used to rely on a third-party "caveman" plugin to keep replies
// terse by re-injecting a style prompt on every turn. That plugin is no
// longer installed, so this file carries the same job, built into the binary
// that already owns the hook wiring: a small, fixed block that
// UserPromptSubmit repeats on every prompt and SessionStart includes once, so
// it survives a context compaction that would otherwise drop it silently.

//go:embed style.md
var styleBody string

// StyleBlock is the reply-style reminder's exact bytes, trailing newline
// trimmed so a caller controls its own spacing when composing it into a
// larger message.
func StyleBlock() string {
	return strings.TrimSuffix(styleBody, "\n")
}

// ReplyStyleEnvVar sets the machine-wide default reply style. "plain" turns
// the block off everywhere on that machine unless a session overrides it back
// to "terse" with `/tdd style terse`; any other value (including unset) keeps
// the built-in terse default.
const ReplyStyleEnvVar = "APHROLLO_REPLY_STYLE"

// effectiveReplyStyle resolves the reply style from an already-loaded session:
// the session's own `/tdd style` override first, then the machine's env
// default, then the built-in "terse".
func effectiveReplyStyle(s *sessionState) string {
	if s != nil && s.Overrides.Style != "" {
		return s.Overrides.Style
	}
	if os.Getenv(ReplyStyleEnvVar) == "plain" {
		return "plain"
	}
	return "terse"
}

// replyStyleFor loads session and resolves its effective reply style. An
// empty or unknown session id loads no state, so it falls through to the env
// default the same as any other session with no override.
func replyStyleFor(session string) string {
	s, _ := loadSession(session)
	return effectiveReplyStyle(s)
}

// setReplyStyle persists the session's `/tdd style` override to disk.
func setReplyStyle(session, style string) error {
	s, path := loadSession(session)
	if s == nil {
		return errNoSession
	}
	s.Overrides.Style = style
	return s.Save(path)
}
