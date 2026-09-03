package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzBashWriteTargets feeds arbitrary bytes as a Bash tool call's command
// string to bashWriteTargets, the classifier behind the primary-checkout Bash
// rule. A Bash command is the one input this whole gate reads that is
// genuinely attacker-shaped from the classifier's point of view: an
// unterminated quote, a heredoc whose delimiter never closes, or a redirect
// with no operand must all resolve to "no write target found" rather than
// hang the shell-word scanner or panic it.
func FuzzBashWriteTargets(f *testing.F) {
	seeds := []string{
		"",
		"echo hi",
		"echo hi > out.txt",
		"cp a.txt /repo/../elsewhere/b.txt",
		"sed -i s/a/b/ file.go",
		"tee -a log.txt",
		"cat <<'EOF'\nsome body > not-a-redirect\nEOF\necho done",
		"cat <<EOF", // unterminated heredoc delimiter
		"echo 'unterminated",
		`echo "unterminated`,
		"mv a b c d e f g h",
		"install -t /tmp a b",
		"2>&1 echo x",
		"echo x 2>&1",
		"echo x > /dev/null",
		"echo x > nul",
		";;;&&||>>>",
		"cp\t\t\ta\t\t\tb",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, cmd string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("bashWriteTargets panicked on cmd=%q: %v", cmd, r)
			}
		}()
		// cwd is a fixed, realistic path: the property under test is the
		// PARSER's robustness, not path resolution (that is the
		// primary-checkout property test's job, over real temp dirs).
		for _, p := range bashWriteTargets(cmd, `/repo/lane`) {
			if p == "" {
				t.Fatalf("bashWriteTargets(%q) returned an empty path in its result", cmd)
			}
			if c := filepath.Clean(p); c != p {
				t.Fatalf("bashWriteTargets(%q) returned %q, not Clean-stable (Clean gives %q)", cmd, p, c)
			}
			// The null sinks (resolveAgainst's own guard) must never survive
			// into a caller's write-target list — deleting that guard lets
			// "/dev/null"/"nul" RESOLVE (join onto cwd, or onto the drive for
			// a rooted "/dev/null") into an ordinary absolute path instead of
			// "", so the check is on the resolved form: a full "/dev/null"
			// path, or a base name of exactly "nul"/"nul:" (the DOS device,
			// however it got prefixed by cwd).
			norm := strings.ToLower(filepath.ToSlash(p))
			last := norm[strings.LastIndexByte(norm, '/')+1:]
			if norm == "/dev/null" || strings.HasSuffix(norm, "/dev/null") || last == "nul" || last == "nul:" {
				t.Fatalf("bashWriteTargets(%q) returned %q — a null sink resolved into a real write target", cmd, p)
			}
		}
	})
}
