package undercover

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
)

// CommitHit names what a commit carries: which field, its value, the tell.
type CommitHit struct {
	Field string
	Value string
	Tell  string
}

// coAuthorTrailer is a Co-authored-by line and its value.
var coAuthorTrailer = regexp.MustCompile(`(?i)^\s*co-authored-by:\s*(.+?)\s*$`)

// CommitTell judges one commit's identities: its author, its committer, and
// every Co-authored-by trailer in its message. A person co-authoring passes;
// the rest of the message is the commit-msg gate's business, not this one's.
func (l List) CommitTell(author, committer, message string) (CommitHit, bool) {
	for _, f := range []struct{ field, value string }{{"author", author}, {"committer", committer}} {
		if tell, hit := l.Ident(f.value); hit {
			return CommitHit{Field: f.field, Value: f.value, Tell: tell}, true
		}
	}
	for _, line := range strings.Split(message, "\n") {
		m := coAuthorTrailer.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if tell, hit := l.Ident(m[1]); hit {
			return CommitHit{Field: "Co-authored-by trailer", Value: m[1], Tell: tell}, true
		}
	}
	return CommitHit{}, false
}

// ConfigIdentity is the identity one git config scope sets.
type ConfigIdentity struct {
	Scope       string // global | repo
	Name, Email string
}

func (c ConfigIdentity) String() string { return c.Name + " <" + c.Email + ">" }

// ConfigIdentities reads user.name and user.email at the global scope and at
// repo's own, in that order, skipping a scope that sets neither (and the
// repo scope of a directory that is not a repository). git runs in the
// caller's environment, so GIT_CONFIG_GLOBAL is honoured.
func ConfigIdentities(repo string) []ConfigIdentity {
	var out []ConfigIdentity
	for _, scope := range []struct{ name, flag string }{{"global", "--global"}, {"repo", "--local"}} {
		id := ConfigIdentity{
			Scope: scope.name,
			Name:  gitConfigValue(repo, scope.flag, "user.name"),
			Email: gitConfigValue(repo, scope.flag, "user.email"),
		}
		if id.Name != "" || id.Email != "" {
			out = append(out, id)
		}
	}
	return out
}

// ConfiguredIdentityTell answers the first configured identity carrying a tell.
func (l List) ConfiguredIdentityTell(repo string) (ConfigIdentity, string, bool) {
	for _, id := range ConfigIdentities(repo) {
		if tell, hit := l.Ident(id.String()); hit {
			return id, tell, true
		}
	}
	return ConfigIdentity{}, "", false
}

// gitConfigValue is one key at one scope, "" when unset or unreadable.
func gitConfigValue(dir, scope, key string) string {
	var stdout bytes.Buffer
	cmd := exec.Command("git", "config", scope, "--get", key)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	if cmd.Run() != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

// RangeTell runs `git log` over rangeArgs in dir, with env as the child's
// environment (nil inherits the caller's), and answers the first commit whose
// identities carry a tell, with its sha.
func (l List) RangeTell(gitBin, dir string, env []string, rangeArgs ...string) (sha string, h CommitHit, hit bool, err error) {
	var stdout bytes.Buffer
	cmd := exec.Command(gitBin, append([]string{"log", "--format=%H%x1f%an <%ae>%x1f%cn <%ce>%x1f%B%x1e"}, rangeArgs...)...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", CommitHit{}, false, err
	}
	for _, rec := range strings.Split(stdout.String(), "\x1e") {
		f := strings.Split(strings.TrimLeft(rec, "\n"), "\x1f")
		if len(f) != 4 {
			continue
		}
		if h, hit := l.CommitTell(f[1], f[2], f[3]); hit {
			return f[0], h, true, nil
		}
	}
	return "", CommitHit{}, false, nil
}
