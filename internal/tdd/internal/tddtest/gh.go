package tddtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The stub is a compiled binary, not a shell script: Windows cannot exec a
// .bat through CreateProcess, so a script stub would simply never run and
// every assertion about gh would pass vacuously. One binary serves every
// test — it reads what to print, and where to record its argv, from the
// environment.
const ghStubSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if log := os.Getenv("GH_STUB_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	if ms := os.Getenv("GH_STUB_SLEEP_MS"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil {
			time.Sleep(time.Duration(n) * time.Millisecond)
		}
	}
	key := ""
	out := ""
	if len(os.Args) > 2 {
		key = "GH_STUB_" + strings.ToUpper(os.Args[1]) + "_" + envName(os.Args[2])
		out = os.Getenv(key)
	}
	// A response can be keyed by the LABEL SET a call asked for, which is how
	// a test states what the real gh does with repeated --label flags: it
	// ANDs them, so a two-label query answers only the issues carrying both.
	if labels := labelKey(key, os.Args); labels != "" {
		if v := os.Getenv(labels); v != "" {
			out = v
		}
	}
	if out == "" {
		out = os.Getenv("GH_STUB_OUT")
	}
	if out != "" {
		fmt.Fprintln(os.Stdout, out)
	}
	if boom := os.Getenv(key + "_FAIL"); boom != "" {
		fmt.Fprintln(os.Stderr, boom)
		os.Exit(1)
	}
}

// labelKey names the env var holding the response for this call's --label
// set, in the order the flags were given: GH_STUB_ISSUE_LIST_LABELS_ESCAPE,
// GH_STUB_ISSUE_LIST_LABELS_ESCAPE_FALSE_POSITIVE, and so on. "" when the
// call passed no label, or when there is no verb/noun key to hang it off.
func labelKey(verbNoun string, args []string) string {
	if verbNoun == "" {
		return ""
	}
	names := []string{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--label" {
			names = append(names, envName(args[i+1]))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return verbNoun + "_LABELS_" + strings.Join(names, "_")
}

// envName upper-cases a label into something an env var can be named after:
// every character outside A-Z0-9 becomes an underscore, so false-positive
// reads as FALSE_POSITIVE.
func envName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}
`

// GhStubDir builds the stub once for the whole package.
var GhStubDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-gh-stub")
	if err != nil {
		return "", err
	}
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(ghStubSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module ghstub\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	name := "gh"
	if hostGOOS == "windows" {
		name = "gh.exe"
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), ".")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the gh stub: %v\n%s", err, out)
	}
	return dir, nil
})

// GhStubKeyPart sanitizes one half of a `<verb> <noun>` call the same way the
// compiled stub's own envName does, so a noun carrying characters an env var
// name cannot hold (a query string's `?`, `&`, `=`) still produces a settable,
// matching key on both sides.
func GhStubKeyPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

// StubGh puts the fake gh on PATH with one canned stdout for every call.
func StubGh(t *testing.T, stdout string) (argvLog string) {
	t.Helper()
	return StubGhScript(t, map[string]string{"": stdout})
}

// StubGhScript puts the fake gh on PATH with one canned response per
// `<verb> <noun>` pair — verify-closure asks gh three different questions in
// one run. The empty key is the catch-all.
func StubGhScript(t *testing.T, responses map[string]string) (argvLog string) {
	t.Helper()
	dir, err := GhStubDir()
	if err != nil {
		t.Fatal(err)
	}
	argvLog = filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("GH_STUB_LOG", argvLog)
	for k, v := range responses {
		if k == "" {
			t.Setenv("GH_STUB_OUT", v)
			continue
		}
		verb, noun, _ := strings.Cut(k, " ")
		t.Setenv("GH_STUB_"+strings.ToUpper(verb)+"_"+GhStubKeyPart(noun), v)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvLog
}

// StubGhFail makes the stub write to stderr and exit non-zero for one
// `<verb> <noun>` pair, which is how gh reports a missing label.
func StubGhFail(t *testing.T, call, message string) {
	t.Helper()
	verb, noun, _ := strings.Cut(call, " ")
	t.Setenv("GH_STUB_"+strings.ToUpper(verb)+"_"+GhStubKeyPart(noun)+"_FAIL", message)
}

// GhArgv is everything the stub recorded in log, "" when it recorded nothing.
func GhArgv(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(data)
}
