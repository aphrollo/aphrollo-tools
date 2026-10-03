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

		"after an escaped quote":  {"echo \"say \\\"hi\\\"\"; sudo foo", "sudo"},
		"timeout":                 {"timeout 30 sudo make install", "sudo"},
		"timeout with flags":      {"timeout -k 5 30 sudo x", "sudo"},
		"timeout by path":         {"/usr/bin/timeout 5 apt-get x", "apt-get"},
		"nice":                    {"nice sudo x", "sudo"},
		"nice -n":                 {"nice -n 10 sudo x", "sudo"},
		"command":                 {"command sudo apt-get x", "sudo"},
		"stdbuf joined":           {"stdbuf -oL sudo x", "sudo"},
		"stdbuf apart":            {"stdbuf -o L apt-get install x", "apt-get"},
		"xargs":                   {"echo foo | xargs -n1 sudo apt-get install", "sudo"},
		"xargs with values":       {"xargs -n 1 -P 4 -I {} sudo x", "sudo"},
		"sh -c":                   {"sh -c \"sudo apt-get update\"", "sudo"},
		"bash -c single":          {"bash -c 'apt-get install x'", "apt-get"},
		"bash -lc":                {"bash -lc \"sudo x\"", "sudo"},
		"bash -o pipefail -c":     {"bash -o pipefail -c \"sudo x\"", "sudo"},
		"sh -c after others":      {"sh -c 'echo ok; sudo x'", "sudo"},
		"nested sh -c":            {"bash -c \"sh -c 'sudo x'\"", "sudo"},
		"a heredoc to bash":       {"bash <<EOF\nsudo apt-get install x\nEOF", "sudo"},
		"a quoted heredoc to sh":  {"sh <<'EOF'\napt-get x\nEOF", "apt-get"},
		"a heredoc piped to bash": {"cat <<EOF | bash\nsudo x\nEOF", "sudo"},
		"bash -s heredoc":         {"bash -s <<EOF\nsudo x\nEOF", "sudo"},
		"py launcher":             {"py -3 -m pip install --user x", "py"},
		"py break system":         {"py -m pip install --break-system-packages x", "py"},
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

		"separators in an escaped-quote string": "echo \"say \\\"; sudo foo; \\\" done\"",
		"command -V":                            "command -V apt",
		"timeout of a test":                     "timeout 30 make test",
		"nice of a test":                        "nice -n 10 go test ./...",
		"xargs echo":                            "xargs -n1 echo sudo",
		"bash -c harmless":                      "bash -c \"echo sudo\"",
		"bash script file":                      "bash script.sh",
		"a heredoc to a file":                   "cat <<EOF > run.sh\nsudo x\nEOF",
		"a heredoc to bash -c":                  "bash -c \"echo\" <<EOF\nsudo x\nEOF",
		"a heredoc to a script":                 "bash script.sh <<EOF\nsudo x\nEOF",
		"py pip install":                        "py -3 -m pip install x",
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

		"an escaped quote":            {`echo "a \"b\" c"`, [][]string{{"echo", `a "b" c`}}},
		"an escaped backslash":        {`echo "a\\b"`, [][]string{{"echo", `a\b`}}},
		"a backslash kept in a quote": {`echo "a\nb"`, [][]string{{"echo", `a\nb`}}},
		"a continuation in a quote":   {"echo \"a\\\nb\"", [][]string{{"echo", "ab"}}},
		"an escaped dollar":           {`echo "\$x"`, [][]string{{"echo", `$x`}}},
		"an escaped backtick":         {"echo \"a\\`b\"", [][]string{{"echo", "a`b"}}},
		"single quotes keep it all":   {`echo 'a\"b'`, [][]string{{"echo", `a\"b`}}},
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

		"fed to a shell":      {"bash <<EOF\nsudo x\nEOF\nafter", "bash <<EOF\nsudo x\nafter"},
		"piped to a shell":    {"cat <<EOF | sh\nbody\nEOF\nafter", "cat <<EOF | sh\nbody\nafter"},
		"a shell with -c":     {"bash -c x <<EOF\nbody\nEOF\nafter", "bash -c x <<EOF\nafter"},
		"a shell with a file": {"bash run.sh <<EOF\nbody\nEOF\nafter", "bash run.sh <<EOF\nafter"},
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

		"timeout":           {[]string{"timeout", "30", "sudo", "x"}, []string{"sudo", "x"}},
		"timeout flags":     {[]string{"timeout", "-k", "5", "-s", "KILL", "30", "apt", "x"}, []string{"apt", "x"}},
		"nice -n":           {[]string{"nice", "-n", "10", "sudo"}, []string{"sudo"}},
		"nice -10":          {[]string{"nice", "-10", "sudo"}, []string{"sudo"}},
		"command":           {[]string{"command", "sudo", "x"}, []string{"sudo", "x"}},
		"command -V":        {[]string{"command", "-V", "sudo"}, []string{"command", "-V", "sudo"}},
		"stdbuf":            {[]string{"stdbuf", "-oL", "-e", "0", "sudo"}, []string{"sudo"}},
		"xargs":             {[]string{"xargs", "-n", "1", "-I", "{}", "sudo"}, []string{"sudo"}},
		"after --":          {[]string{"xargs", "--", "sudo"}, []string{"sudo"}},
		"wrappers nested":   {[]string{"nice", "timeout", "5", "env", "A=1", "sudo"}, []string{"sudo"}},
		"a wrapper alone":   {[]string{"timeout"}, nil},
		"timeout no cmd":    {[]string{"timeout", "30"}, nil},
		"a path wrapper":    {[]string{"/usr/bin/nice", "sudo"}, []string{"sudo"}},
		"a flag value ends": {[]string{"nice", "-n"}, nil},
	} {
		if got := commandStart(tc.words); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: commandStart(%q) = %q, want %q", name, tc.words, got, tc.want)
		}
	}
}

func TestShellUsage_AStringAFileOrStdinAndNeverARedirectionAsAFile(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd  []string
		use  shellUse
		body string
	}{
		"not a shell":            {[]string{"python", "-c", "x"}, notShell, ""},
		"bare":                   {[]string{"bash"}, shellStdin, ""},
		"-s":                     {[]string{"bash", "-s"}, shellStdin, ""},
		"-c":                     {[]string{"bash", "-c", "x y"}, shellString, "x y"},
		"-lc":                    {[]string{"bash", "-lc", "x"}, shellString, "x"},
		"-c without a script":    {[]string{"bash", "-c"}, shellStdin, ""},
		"-o value then -c":       {[]string{"bash", "-o", "pipefail", "-c", "x"}, shellString, "x"},
		"-O value then a file":   {[]string{"bash", "-O", "extglob", "run.sh"}, shellFile, ""},
		"a file":                 {[]string{"sh", "run.sh"}, shellFile, ""},
		"a long flag then file":  {[]string{"bash", "--norc", "run.sh"}, shellFile, ""},
		"a plus flag then -c":    {[]string{"bash", "+x", "-c", "y"}, shellString, "y"},
		"a heredoc":              {[]string{"bash", "<<EOF"}, shellStdin, ""},
		"a redirect and target":  {[]string{"bash", "<", "in.sh"}, shellStdin, ""},
		"an output redirect":     {[]string{"bash", ">", "out"}, shellStdin, ""},
		"a joined redirect":      {[]string{"bash", "2>&1"}, shellStdin, ""},
		"a long flag is no -c":   {[]string{"bash", "--noprofile"}, shellStdin, ""},
		"a path to the shell":    {[]string{"/bin/bash", "-c", "x"}, shellString, "x"},
		"a flag with a c in it":  {[]string{"bash", "--rcfile", "x"}, shellFile, ""},
		"an option that has a c": {[]string{"bash", "-e", "-c", "x"}, shellString, "x"},
	} {
		use, body := shellUsage(tc.cmd)
		if use != tc.use || body != tc.body {
			t.Errorf("%s: shellUsage(%q) = %v, %q, want %v, %q", name, tc.cmd, use, body, tc.use, tc.body)
		}
	}
}

func TestWrapperSkip_FlagsTheirValuesAndThePositionalsBeforeTheCommand(t *testing.T) {
	timeout := wrappers["timeout"]
	for name, tc := range map[string]struct {
		w    wrapper
		rest []string
		own  int
		runs bool
	}{
		"no flags":                 {timeout, []string{"30", "x"}, 1, true},
		"a flag with a value":      {timeout, []string{"-k", "5", "30", "x"}, 3, true},
		"a flag without a value":   {timeout, []string{"--foreground", "30", "x"}, 2, true},
		"-- ends the flags":        {wrappers["xargs"], []string{"--", "-n", "x"}, 1, true},
		"a value past the end":     {wrappers["nice"], []string{"-n"}, 1, true},
		"positionals past the end": {timeout, nil, 0, true},
		"a lookup":                 {wrappers["command"], []string{"-v", "x"}, 0, false},
		"command runs":             {wrappers["command"], []string{"x"}, 0, true},
		"a flag after the command": {wrappers["nice"], []string{"x", "-n", "5"}, 0, true},
	} {
		own, runs := tc.w.skip(tc.rest)
		if own != tc.own || runs != tc.runs {
			t.Errorf("%s: skip(%q) = %d, %v, want %d, %v", name, tc.rest, own, runs, tc.own, tc.runs)
		}
	}
}

func TestFeedsShell_OnlyAShellThatReadsItsScriptFromStdin(t *testing.T) {
	for line, want := range map[string]bool{
		"bash <<EOF":                 true,
		"cat <<EOF | sh":             true,
		"sudo bash <<EOF":            false, // sudo is the command; sudo itself is refused
		"bash -c x <<EOF":            false,
		"bash run.sh <<EOF":          false,
		"cat <<EOF > run.sh":         false,
		"then":                       false,
		"A=1":                        false,
		"":                           false,
		"timeout 5 bash -s <<EOF":    true,
		"python3 <<EOF":              false,
		"echo \"bash\" <<EOF":        false,
		"if true; then bash <<EOF":   true,
		"cat <<EOF | timeout 5 bash": true,
		"cat <<EOF | grep bash":      false,
	} {
		if got := feedsShell(line); got != want {
			t.Errorf("feedsShell(%q) = %v, want %v", line, got, want)
		}
	}
}
