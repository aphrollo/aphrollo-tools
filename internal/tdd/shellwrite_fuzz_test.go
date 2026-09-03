package tdd

import "testing"

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
		_ = bashWriteTargets(cmd, `/repo/lane`)
	})
}
