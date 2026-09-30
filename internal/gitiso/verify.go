package gitiso

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ProbeEnv is set in the environment of the child run VerifyNoLeak starts; a
// package's probe test does nothing without it.
const ProbeEnv = "APHROLLO_GITISO_PROBE"

// probeTimeout bounds the child run: it starts a whole test binary, whose own
// TestMain may build stubs first.
const probeTimeout = 5 * time.Minute

// Probe is the body of a package's git-isolation probe test. Run by
// VerifyNoLeak in a child process, it does what a careless fixture does: bare
// git calls that name no repository and no home. None may find a repository
// that is not the one it makes, and everything it writes must land under the
// child's own temp root. Run on its own it does nothing.
func Probe(t *testing.T) {
	t.Helper()
	if os.Getenv(ProbeEnv) == "" {
		// skip-ok: the probe only means something inside VerifyNoLeak's hostile child.
		t.Skip("git isolation probe: only runs inside VerifyNoLeak")
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_DIR") || name == "GIT_WORK_TREE" || name == "GIT_INDEX_FILE" || name == "GIT_COMMON_DIR" {
			t.Errorf("the run still carries %s", kv)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	git := func(dir string, args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("git %v in %s: %v: %s", args, dir, err, strings.TrimSpace(string(out)))
		}
		return err
	}
	for _, dir := range []string{cwd, base} {
		if err := git(dir, "rev-parse", "--git-dir"); err == nil {
			t.Errorf("git found a repository from %s: a bare git call there acts on it", dir)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "--bare", "dest"},
		{"init", "-q", "work"},
	} {
		if err := git(base, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	work := filepath.Join(base, "work")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "user.name", "probe"},
		{"config", "user.email", "probe@example.com"},
		{"add", "-A"},
		{"commit", "-q", "-m", "probe"},
		{"remote", "add", "dest", filepath.Join(base, "dest")},
		{"config", "--global", "user.name", "Probe Person"},
	} {
		if err := git(work, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}

// VerifyNoLeak runs the calling package's own test binary twice, restricted to
// the probe test named probe, in an environment built to reach a real
// repository and a real global config, and fails when either changed. Once with
// the variables a git hook exports (GIT_DIR, GIT_INDEX_FILE) and
// a global config in the environment; once with none, from a directory inside
// the repository and a TMPDIR inside it. The package's TestMain is what stands
// between the probe and the victim, so this proves that TestMain.
func VerifyNoLeak(t *testing.T, probe string) {
	t.Helper()
	for _, scenario := range []struct {
		name string
		hook bool
	}{{"hook environment", true}, {"repository around the run", false}} {
		t.Run(scenario.name, func(t *testing.T) {
			victim := makeVictim(t)
			hostileHome := t.TempDir()
			hostileGlobal := filepath.Join(hostileHome, ".gitconfig")
			if err := os.WriteFile(hostileGlobal, []byte("[user]\n\tname = Real Person\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			beforeState, beforeText := victimState(victim), victimText(victim)
			beforeGlobal, err := os.ReadFile(hostileGlobal)
			if err != nil {
				t.Fatal(err)
			}

			env := hostileEnv(hostileHome, victim, scenario.hook)
			dir := filepath.Join(victim, "pkg")
			if scenario.hook {
				dir = t.TempDir()
			}
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+probe+"$", "-test.count=1", "-test.v")
			cmd.Dir = dir
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "--- PASS: "+probe) {
				t.Errorf("the probe did not pass (%v):\n%s", err, out)
			}
			if victimState(victim) != beforeState {
				t.Errorf("the run changed the repository around it:\nbefore\n%s\nafter\n%s", beforeText, victimText(victim))
			}
			if got, _ := os.ReadFile(hostileGlobal); string(got) != string(beforeGlobal) {
				t.Errorf("the run changed the global git config it was handed:\n%s", got)
			}
		})
	}
}

// makeVictim is a repository with a commit and a package directory, standing in
// for the checkout a test binary is started inside.
func makeVictim(t *testing.T) string {
	t.Helper()
	victim := t.TempDir()
	if err := os.MkdirAll(filepath.Join(victim, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "pkg", "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=v", "-c", "user.email=v@example.com", "add", "-A"},
		{"-c", "user.name=v", "-c", "user.email=v@example.com", "commit", "-q", "-m", "victim"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = victim
		cmd.Env = cleanedEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return victim
}

// cleanedEnv is this process's environment with every GIT_* variable dropped.
func cleanedEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(name, "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

// hostileEnv is the environment of the probe's child: this process's, with the
// home and git variables replaced by ones that lead to victim and to a real
// global config. With hook set the child gets what a git hook exports;
// otherwise it gets a TMPDIR inside the victim repository and nothing that
// names it.
func hostileEnv(home, victim string, hook bool) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "GIT_"), name == "HOME", name == "USERPROFILE", name == "TMPDIR", name == "TMP", name == "TEMP", name == "GOTMPDIR",
			strings.HasPrefix(name, "XDG_"), name == "APPDATA", name == "LOCALAPPDATA":
		default:
			env = append(env, kv)
		}
	}
	env = append(env, ProbeEnv+"=1", "HOME="+home, "USERPROFILE="+home)
	if hook {
		return append(env,
			"GIT_DIR="+filepath.Join(victim, ".git"),
			"GIT_INDEX_FILE="+filepath.Join(victim, ".git", "index"),
			"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"))
	}
	tmp := filepath.Join(victim, "tmp")
	_ = os.MkdirAll(tmp, 0o755)
	return append(env, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
}

// victimText is what a leaked fixture writes to a repository and can be read as
// text: its config, its HEAD and the refs it holds.
func victimText(victim string) string {
	var sb strings.Builder
	for _, name := range []string{"config", "HEAD"} {
		data, _ := os.ReadFile(filepath.Join(victim, ".git", name))
		sb.WriteString(name + ":\n" + string(data) + "\n")
	}
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname) %(objectname)")
	cmd.Dir = victim
	cmd.Env = cleanedEnv()
	refs, _ := cmd.CombinedOutput()
	sb.WriteString("refs:\n" + string(refs))
	return sb.String()
}

// victimState is victimText and the index, which a leaked `git add` replaces.
func victimState(victim string) string {
	index, _ := os.ReadFile(filepath.Join(victim, ".git", "index"))
	return victimText(victim) + "\nindex:\n" + string(index)
}
