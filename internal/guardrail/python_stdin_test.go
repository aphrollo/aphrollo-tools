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
		{"dash script, no redirect", "python - ", false},
		{"inside a quoted string", `echo "python - < /dev/null"`, false},
		{"inside single quotes", `grep 'python - < /dev/null' notes.md`, false},
		{"-c is no REPL", `python -c "print(1)" < /dev/null`, false},
		{"-m is no REPL", "python -m http.server < /dev/null", false},
		{"version query", "python --version < /dev/null", false},
		{"redirect belongs to an earlier command", "echo hi < /dev/null; python - <<'EOF'\nprint(1)\nEOF", false},
		{"redirect of stderr only", "python - 2< /dev/null", false},
		{"heredoc wins when it comes last", "python - < /dev/null <<'EOF'\nprint(1)\nEOF", false},
		{"redirect text inside the heredoc body", "python - <<'EOF'\nprint('x' < /dev/null)\nEOF", false},
		{"another program", "ruby - < /dev/null", false},
		{"python only as an argument", "echo python - < /dev/null", false},
	}
	for _, c := range cases {
		if got := pythonReadsNullStdin(c.command); got != c.want {
			t.Errorf("%s: pythonReadsNullStdin(%q) = %v, want %v", c.name, c.command, got, c.want)
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
	for _, part := range []string{"python-stdin-null", "REPL", "python file.py < /dev/null", "heredoc"} {
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
