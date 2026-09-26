package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// identitySessionRepo is a git repo declaring undercover (or not), with git's
// global identity set to globalIdent's name and address in an isolated
// global config.
func identitySessionRepo(t *testing.T, on bool, name, email string) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = "+name+"\n\temail = "+email+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	manifest := "[aphrollo]\n"
	if on {
		manifest += "undercover = true\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// A session restart reset git's global identity to the tool's own, and the
// commits made after it carried that name onto main. The session start is
// the first moment to say so, and it says it first.
func TestHandleSessionStart_OpensWithATellIdentityAndItsFix(t *testing.T) {
	repo := identitySessionRepo(t, true, "Claude", "noreply@anthropic.com")
	msg := HandleSessionStart(mustJSON(t, map[string]string{"session_id": "ss-ident", "cwd": repo}))
	first := strings.SplitN(msg, "\n", 2)[0]
	for _, want := range []string{"undercover", "Claude <noreply@anthropic.com>", "git config --global user.name", "git config --global user.email"} {
		if !strings.Contains(first, want) {
			t.Errorf("first line %q must name %q", first, want)
		}
	}
}

func TestHandleSessionStart_SaysNothingAboutAPersonsIdentity(t *testing.T) {
	repo := identitySessionRepo(t, true, "Claudia Airey", "claudia@example.com")
	msg := HandleSessionStart(mustJSON(t, map[string]string{"session_id": "ss-ident-ok", "cwd": repo}))
	if strings.Contains(msg, "UNDERCOVER IDENTITY") {
		t.Errorf("an ordinary identity was reported:\n%s", msg)
	}
}

func TestHandleSessionStart_IgnoresTheIdentityWhenUndercoverIsOff(t *testing.T) {
	repo := identitySessionRepo(t, false, "Claude", "noreply@anthropic.com")
	msg := HandleSessionStart(mustJSON(t, map[string]string{"session_id": "ss-ident-off", "cwd": repo}))
	if strings.Contains(msg, "noreply@anthropic.com") {
		t.Errorf("a repo that never opted in was told about its identity:\n%s", msg)
	}
}
