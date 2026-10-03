package guardrail

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPythonReadsNullStdin_JudgesTheCommandByItsParse(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"dash script, dev null", "python - < /dev/null", true},
		{"python3, no space before the path", "python3 -</dev/null", true},
		{"py launcher with version flag, NUL", "py -3 - < NUL", true},
		{"lowercase nul", "python - < nul", true},
		{"explicit fd 0", "python - 0< /dev/null", true},
		{"exe suffix and path", "C:/Python312/python.exe - < /dev/null", true},
		{"no script argument is the REPL", "python < /dev/null", true},
		{"unbuffered flag before the dash", "python -u - < /dev/null", true},
		{"env assignment before the command", "PYTHONUTF8=1 python - < /dev/null", true},
		{"every name character class", "AZaz09_=1 python - < /dev/null", true},
		{"an escaped dash is still the dash", `python \- < /dev/null`, true},
		{"stderr duplicated onto stdout", "python - 2>&1 < /dev/null", true},
		{"both streams to a file", "python - &>out.log < /dev/null", true},
		{"stdout to a file", "python - > out.log < /dev/null", true},
		{"after a pipe", "cat x.py | python - < /dev/null", true},
		{"quoted dash is still the dash", `python "-" < /dev/null`, true},
		{"a quoted word holds no operator", `python - "a<b" < /dev/null`, true},
		{"after a background job", "sleep 1 & python - < /dev/null", true},
		{"heredoc, redirect on the opening line", "python3 - <<'EOF' < /dev/null\nprint(1)\nEOF", true},
		{"heredoc, redirect on a line after the terminator", "python3 - <<'EOF'\nprint(1)\nEOF\n < /dev/null", true},
		{"redirect set after the heredoc wins", "python - <<EOF < /dev/null\nprint(1)\nEOF", true},

		{"script file", "python file.py < /dev/null", false},
		{"script file after a flag with a value", "python -W ignore file.py < /dev/null", false},
		{"heredoc, no redirect", "python - <<'EOF'\nprint(1)\nEOF", false},
		{"dash script reading a real file", "python - < input.txt", false},
		{"inside a quoted string", `echo "python - < /dev/null"`, false},
		{"inside single quotes", `grep 'python - < /dev/null' notes.md`, false},
		{"-c is no REPL", `python -c "print(1)" < /dev/null`, false},
		{"-m is no REPL", "python -m http.server < /dev/null", false},
		{"version query", "python --version < /dev/null", false},
		{"redirect belongs to an earlier command", "echo hi < /dev/null; python - <<'EOF'\nprint(1)\nEOF", false},
		{"heredoc wins when it comes last", "python - < /dev/null <<'EOF'\nprint(1)\nEOF", false},
		{"redirect text inside the heredoc body", "python - <<'EOF'\nprint('x' < /dev/null)\nEOF", false},
		{"another program", "ruby - < /dev/null", false},
		{"python only as an argument", "echo python - < /dev/null", false},
		{"bare dash, no stdin source", "python3 -", true},
		{"bare dash with trailing space", "python - ", true},
		{"bare dash, py launcher", "py -3 -", true},
		{"bare python is the REPL", "python3", true},
		{"unbuffered flag only is the REPL", "python -u", true},
		{"after a cd and &&", "cd x && python3 -", true},
		{"after a semicolon past a pipe", "cat a | cat; python -", true},
		{"after || past a pipe", "cat a | cat || python -", true},
		{"after && past a pipe", "cat a | cat && python -", true},
		{"after a newline past a pipe", "cat a | cat\npython -", true},
		{"in a subshell", "(cd x && python3 -)", true},
		{"in a command substitution", "echo $(python3 -)", true},
		{"in backticks", "echo `python3 -`", true},
		{"in a brace group", "{ python3 -; }", true},
		{"in an if branch", "if true; then python3 -; fi", true},
		{"behind exec", "exec python3 -", true},
		{"behind time", "time python3 -", true},
		{"behind a negation", "! python3 -", true},
		{"in a loop body", "while true; do python3 -; done", true},
		{"in an else branch", "if false; then true; else python3 -; fi", true},
		{"left side of a pipe", "python3 - | cat", true},
		{"left side of a pipe in a subshell", "(python3 -) | cat", true},
		{"env assignment, no stdin", "PYTHONUTF8=1 python3 -", true},
		{"stdout redirect is no stdin source", "python3 - > out.txt 2>&1", true},
		{"stderr-only redirect is no stdin source", "python - 2< /dev/null", true},
		{"empty heredoc, quoted delimiter", "python3 - <<'EOF'\nEOF", true},
		{"empty heredoc, bare delimiter", "python3 - <<EOF\nEOF", true},
		{"empty heredoc, dash form, indented terminator", "python3 - <<-EOF\n	EOF", true},
		{"empty heredoc in a chain", "cd x && python3 - <<'EOF'\nEOF", true},
		{"empty heredoc then more commands", "python3 - <<'EOF'\nEOF\necho done", true},
		{"empty heredoc after a full one", "python3 - <<'EOF'\nprint(1)\nEOF\npython3 - <<'EOF'\nEOF", true},
		{"last of two heredocs is empty", "python3 - <<A <<B\nprint(1)\nA\nB", true},

		{"piped input", "cat s.py | python -", false},
		{"piped input, bare python", "echo 'print(1)' | python3", false},
		{"piped input through stderr pipe", "cat s.py |& python -", false},
		{"piped input, pipe at end of line", "cat s.py |\npython -", false},
		{"piped input in a long pipeline", "cat s.py | tr a b | python3 -", false},
		{"piped input into a subshell", "cat s.py | (python -)", false},
		{"piped input into a brace group", "cat s.py | { python -; }", false},
		{"piped input, python after a subshell pipe", "(echo x | python -)", false},
		{"heredoc with a body line", "python3 - <<'EOF'\nprint(1)\nEOF", false},
		{"heredoc whose body is a blank line is data", "python3 - <<'EOF'\n\nEOF", false},
		{"heredoc in a chain", "cd x && python3 - <<'EOF'\nprint(1)\nEOF", false},
		{"heredoc with the dash form", "python3 - <<-EOF\n	print(1)\n	EOF", false},
		{"first of two heredocs is empty, the last has a body", "python3 - <<A <<B\nA\nprint(1)\nB", false},
		{"heredoc whose terminator never comes", "python3 - <<'EOF'\nprint(1)", false},
		{"here-string", "python3 - <<< 'print(1)'", false},
		{"empty here-string", `python3 - <<< ""`, false},
		{"process substitution", "python3 - < <(echo 'print(1)')", false},
		{"descriptor duplicated onto stdin", "python3 - <&3", false},
		{"python after an earlier empty heredoc of another command", "cat <<'EOF'\nEOF\npython3 - <<'EOF'\nprint(1)\nEOF", false},
		{"quoted bare dash", `echo "python3 -"`, false},
		{"quoted python name", `echo 'python3'`, false},
		{"python only as an operand", "which python3", false},
		{"python only as a file name", "ls python3 -l", false},
		{"in a comment", "echo hi # python3 -", false},
		{"in a comment after a semicolon", "echo hi # a; python3 -", false},
		{"a comment line", "# python3 -", false},
		{"script file, no redirect", "python3 script.py", false},
		{"script file that takes a dash", "python3 script.py -", false},
		{"-c, no redirect", `python3 -c "print(1)"`, false},
		{"-m, no redirect", "python3 -m pytest", false},
		{"version, no redirect", "python3 --version", false},
		{"help, no redirect", "python3 --help", false},
		{"launcher list", "py --list", false},
		{"launcher list paths", "py -0p", false},
	}
	for _, c := range cases {
		if got := pythonStdinHangs(c.command); got != c.want {
			t.Errorf("%s: pythonStdinHangs(%q) = %v, want %v", c.name, c.command, got, c.want)
		}
	}
}

func withNullStdinTTY(t *testing.T, on bool) {
	t.Helper()
	prev := nullStdinIsTTY
	nullStdinIsTTY = on
	t.Cleanup(func() { nullStdinIsTTY = prev })
}

func TestEvaluate_BlocksPythonDashOnANullStdinWhereItIsATTY(t *testing.T) {
	withNullStdinTTY(t, true)

	d := Evaluate("Bash", "python3 - <<'EOF' < /dev/null\nprint(1)\nEOF")

	if d.Action != Block {
		t.Fatalf("action %v, want Block", d.Action)
	}
	for _, part := range []string{"python-stdin-null", "REPL", "python file.py", "heredoc"} {
		if !strings.Contains(d.Reason, part) {
			t.Errorf("reason %q lacks %q", d.Reason, part)
		}
	}
	if n := len(strings.Fields(d.Reason)); n > 80 {
		t.Errorf("reason is %d words, want a short one", n)
	}
}

func TestEvaluate_AllowsPythonDashOnANullStdinWhereItIsNotATTY(t *testing.T) {
	withNullStdinTTY(t, false)

	d := Evaluate("Bash", "python - < /dev/null")

	if d.Action != Allow {
		t.Fatalf("action %v, want Allow: off Windows /dev/null is no terminal", d.Action)
	}
}

func TestEvaluate_LetsAPythonScriptFileThroughOnANullStdin(t *testing.T) {
	withNullStdinTTY(t, true)

	d := Evaluate("Bash", "python file.py < /dev/null")

	if d.Action != Allow {
		t.Fatalf("action %v, want Allow", d.Action)
	}
}

func TestDecideFromHookInput_BlocksThePythonNullStdinHang(t *testing.T) {
	withNullStdinTTY(t, true)
	m := recordedHook(t, "pretooluse_bash.json")
	m["tool_input"].(map[string]any)["command"] = "python - < /dev/null"
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	got, err := DecideFromHookInput(raw)

	if err != nil || got.Action != Block {
		t.Fatalf("action %v, err %v, want Block", got.Action, err)
	}
}

func TestEvaluate_BlocksABareDashWhereTheHarnessGivesNoStdin(t *testing.T) {
	withNullStdinTTY(t, true)

	for _, command := range []string{"python3 -", "cd x && python -", "python3 - <<'EOF'\nEOF"} {
		d := Evaluate("Bash", command)

		if d.Action != Block {
			t.Errorf("Evaluate(%q) action %v, want Block", command, d.Action)
			continue
		}
		for _, part := range []string{"python-stdin-null", "REPL", "python file.py"} {
			if !strings.Contains(d.Reason, part) {
				t.Errorf("Evaluate(%q) reason %q lacks %q", command, d.Reason, part)
			}
		}
		if n := len(strings.Fields(d.Reason)); n > 80 {
			t.Errorf("reason is %d words, want a short one", n)
		}
	}
}

func TestEvaluate_AllowsABareDashWhereAPipeOrAHeredocFeedsIt(t *testing.T) {
	withNullStdinTTY(t, true)

	for _, command := range []string{"cat s.py | python -", "python - <<'EOF'\nprint(1)\nEOF"} {
		if d := Evaluate("Bash", command); d.Action != Allow {
			t.Errorf("Evaluate(%q) action %v, want Allow", command, d.Action)
		}
	}
}

func TestEvaluate_AllowsABareDashOffWindows(t *testing.T) {
	withNullStdinTTY(t, false)

	if d := Evaluate("Bash", "python3 -"); d.Action != Allow {
		t.Fatalf("action %v, want Allow: off Windows the harness stdin reads end of file", d.Action)
	}
}

func TestDecideFromHookInput_BlocksABareDash(t *testing.T) {
	withNullStdinTTY(t, true)

	got, err := DecideFromHookInput([]byte(`{"tool_name":"Bash","tool_input":{"command":"python3 -"}}`))

	if err != nil || got.Action != Block {
		t.Fatalf("action %v, err %v, want Block", got.Action, err)
	}
}
