package ghworkflow

import (
	"reflect"
	"strings"
	"testing"
)

// A step whose command changes the box outside anything an environment
// variable can redirect is refused before it runs. These pin which commands
// those are, and, as sharply, which look like them and are not.

func TestRefuseGlobal_RefusesWhatChangesTheBoxAndNamesTheCommand(t *testing.T) {
	for name, tc := range map[string]struct{ script, named string }{
		"sudo":                 {"sudo apt-get install -y libfoo", "sudo"},
		"bare apt-get":         {"apt-get install -y libfoo", "apt-get"},
		"after &&":             {"echo hi && sudo make install", "sudo"},
		"after then":           {"if true; then sudo systemctl restart x; fi", "sudo"},
		"in an if condition":   {"if sudo -n true; then echo ok; fi", "sudo"},
		"behind env":           {"env DEBIAN_FRONTEND=noninteractive sudo apt install foo", "sudo"},
		"behind assignments":   {"FOO=1 BAR=2 sudo -E cmd", "sudo"},
		"by path":              {"/usr/bin/sudo cmd", "sudo"},
		"in a substitution":    {"x=$(sudo cat /etc/shadow)", "sudo"},
		"after a continuation": {"echo a \\\n  && sudo y", "sudo"},
		"third line":           {"echo 1\necho 2\nsudo three", "sudo"},
		"after a here-string":  {"cat <<< hi\nsudo foo", "sudo"},
		"pip --user":           {"pip install --user requests", "pip"},
		"pip3 --user":          {"pip3 install --user requests", "pip3"},
		"python -m pip":        {"python -m pip install --user requests", "python"},
		"break system":         {"python3 -m pip install --break-system-packages x", "python3"},
		"uv --system":          {"uv pip install --system x", "uv"},
		"yarn global":          {"yarn global add typescript", "yarn"},
		"pnpm -g":              {"pnpm add -g typescript", "pnpm"},
		"pnpm --global":        {"pnpm install --global x", "pnpm"},
		"gem install":          {"gem install bundler", "gem"},
		"corepack enable":      {"corepack enable", "corepack"},
		"brew":                 {"brew install jq", "brew"},
		"choco":                {"choco install nodejs", "choco"},
		"winget":               {"winget install Git.Git", "winget"},
	} {
		why := refuseGlobal(tc.script)
		if why == "" {
			t.Errorf("%s: %q was not refused", name, tc.script)
			continue
		}
		if !strings.Contains(why, tc.named) {
			t.Errorf("%s: the refusal %q does not name %q", name, why, tc.named)
		}
	}
}

func TestRefuseGlobal_LeavesWhatOnlyLooksLikeAnInstallAlone(t *testing.T) {
	for name, script := range map[string]string{
		"a word in an echo":         "echo sudo is not used",
		"a comment":                 "# sudo apt-get install foo\necho ok",
		"a trailing comment":        "echo ok # sudo apt-get install foo",
		"a quoted command":          `echo "run sudo make install"`,
		"command -v":                "command -v apt-get >/dev/null && echo yes",
		"which":                     "which sudo",
		"grep":                      "grep -r sudo .",
		"a heredoc body":            "cat <<EOF > run.sh\nsudo apt-get install foo\nEOF\necho done",
		"a quoted heredoc body":     "cat <<'EOF'\nsudo x\nEOF",
		"an indented heredoc":       "cat <<-EOF\n\tsudo x\n\tEOF\n",
		"pip requirements":          "pip install -r requirements.txt",
		"pip upgrade":               "python -m pip install --upgrade pip",
		"pip list --user":           "pip list --user",
		"npm -g, contained":         "npm install -g typescript",
		"go install, contained":     "go install ./cmd/...",
		"cargo install, contained":  "cargo install ripgrep",
		"a variable named like one": `echo "${sudo}"`,
		"yarn add":                  "yarn add left-pad",
		"pnpm install":              "pnpm install --frozen-lockfile",
		"corepack prepare":          "corepack prepare pnpm@9 --activate",
	} {
		if why := refuseGlobal(script); why != "" {
			t.Errorf("%s: %q was refused: %s", name, script, why)
		}
	}
}

func TestRefuseGlobal_ACommandWithoutItsArgumentsIsNotAnInstall(t *testing.T) {
	for _, script := range []string{"yarn", "pnpm", "gem", "corepack", "pip", "python -m", "python3 -m pip", "uv"} {
		if why := refuseGlobal(script); why != "" {
			t.Errorf("%q was refused: %s", script, why)
		}
	}
}

func TestShellCommands_SplitsWordsQuotesAndCommandsExactly(t *testing.T) {
	for name, tc := range map[string]struct {
		script string
		want   [][]string
	}{
		"words":                {"echo a b", [][]string{{"echo", "a", "b"}}},
		"a tab splits":         {"echo a\tb", [][]string{{"echo", "a", "b"}}},
		"a carriage return":    {"echo a\r\necho b", [][]string{{"echo", "a"}, {"echo", "b"}}},
		"quotes group":         {`echo "a b" 'c d'`, [][]string{{"echo", "a b", "c d"}}},
		"an empty quote":       {`echo ""`, [][]string{{"echo", ""}}},
		"separators":           {"a;b&&c||d|e", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}},
		"a continuation":       {"echo a \\\n  b", [][]string{{"echo", "a", "b"}}},
		"a backslash kept":     {`C:\tools\x`, [][]string{{`C:\tools\x`}}},
		"a comment":            {"echo a # note\necho b", [][]string{{"echo", "a"}, {"echo", "b"}}},
		"a hash in a word":     {"echo a#b", [][]string{{"echo", "a#b"}}},
		"blank lines":          {"\n\n  \n", nil},
		"a substitution":       {"x=$(sudo y)", [][]string{{"x=$"}, {"sudo", "y"}}},
		"a quoted separator":   {`echo "a;b"`, [][]string{{"echo", "a;b"}}},
		"a quote after a hash": {`echo "# x" y`, [][]string{{"echo", "# x", "y"}}},
	} {
		if got := shellCommands(tc.script); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: shellCommands(%q) = %q, want %q", name, tc.script, got, tc.want)
		}
	}
}

func TestWithoutHeredocs_DropsBodiesWithTheirTerminatorsAndNothingElse(t *testing.T) {
	for name, tc := range map[string]struct{ script, want string }{
		"a body":              {"cat <<EOF\nbody\nEOF\nafter", "cat <<EOF\nafter"},
		"an empty body":       {"cat <<EOF\nEOF\nafter", "cat <<EOF\nafter"},
		"no terminator":       {"cat <<EOF\nbody", "cat <<EOF\nbody"},
		"a longer terminator": {"cat <<EOF\nEOF2\nEOF\nafter", "cat <<EOF\nafter"},
		"quoted":              {"cat <<'EOF'\nbody\nEOF\nafter", "cat <<'EOF'\nafter"},
		"double quoted":       {"cat <<\"END\"\nbody\nEND\nafter", "cat <<\"END\"\nafter"},
		"indented":            {"cat <<-EOF\n\tbody\n\tEOF\nafter", "cat <<-EOF\nafter"},
		"a here-string":       {"cat <<< hi\nnext", "cat <<< hi\nnext"},
		"no heredoc":          {"echo a\necho b", "echo a\necho b"},
		"text before":         {"echo a\ncat <<EOF\nx\nEOF", "echo a\ncat <<EOF"},
	} {
		if got := withoutHeredocs(tc.script); got != tc.want {
			t.Errorf("%s: withoutHeredocs(%q) = %q, want %q", name, tc.script, got, tc.want)
		}
	}
}

func TestBaseName_IsTheCommandAPersonWouldSay(t *testing.T) {
	for in, want := range map[string]string{
		"sudo":                         "sudo",
		"/usr/bin/sudo":                "sudo",
		`C:\Windows\System32\Sudo.EXE`: "sudo",
		"/sudo":                        "sudo",
		"apt-get.cmd":                  "apt-get",
		"run.bat":                      "run",
		"":                             "",
	} {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandStart_PassesOverWhatStandsBeforeTheCommand(t *testing.T) {
	for name, tc := range map[string]struct{ words, want []string }{
		"nothing before":    {[]string{"sudo", "x"}, []string{"sudo", "x"}},
		"assignments":       {[]string{"A=1", "B=2", "sudo", "x"}, []string{"sudo", "x"}},
		"a wrapper's flag":  {[]string{"env", "-i", "A=1", "apt", "x"}, []string{"apt", "x"}},
		"a keyword":         {[]string{"if", "sudo", "-n"}, []string{"sudo", "-n"}},
		"command -v":        {[]string{"command", "-v", "apt-get"}, []string{"command", "-v", "apt-get"}},
		"a flag first":      {[]string{"-v", "x"}, []string{"-v", "x"}},
		"only assignments":  {[]string{"A=1"}, nil},
		"only a keyword":    {[]string{"then"}, nil},
		"empty":             {nil, nil},
		"not an assignment": {[]string{"=x", "y"}, []string{"=x", "y"}},
	} {
		if got := commandStart(tc.words); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: commandStart(%q) = %q, want %q", name, tc.words, got, tc.want)
		}
	}
}
