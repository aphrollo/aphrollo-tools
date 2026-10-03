package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// requiringRepo is a directory that looks like a checkout (a .git entry) and
// declares the oldest binary it accepts, with the undercover switch on so a
// hook that DOES run has something to refuse.
func requiringRepo(t *testing.T, requires string) string {
	t.Helper()
	// The tests here are about a release build meeting or missing a minimum; a
	// dev build (what a test binary is) meets any, and has its own test below.
	buildinfo.SetVersionForTest("1.0.0")
	t.Cleanup(func() { buildinfo.SetVersionForTest("") })
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nundercover = true\nrequires = \""+requires+"\"\n")
	return repo
}

// tooOldLine is the one line a repo requiring >=99.0 gets from this binary.
func tooOldLine() string {
	return "aphrollo too old here: this repo requires >=99.0, this is " + buildinfo.Version() + "; run aphrollo update"
}

const attributionTrailer = "Fix the retry timer\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"

func commitMessageIn(t *testing.T, repo string) string {
	t.Helper()
	msg := filepath.Join(repo, "COMMIT_EDITMSG")
	writeFile(t, msg, attributionTrailer)
	return msg
}

func runCLI(args []string, stdin string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// An older binary must not judge a repo whose gate it cannot read: a git hook
// lets the commit through, says why on one line, and the gate stays out of it.
func TestCompatGuard_AGitHookInATooOldRepoFailsOpenWithOneLine(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	msg := commitMessageIn(t, repo)

	code, stdout, stderr := runCLI([]string{"gate", "commitmsg", msg, "--repo", repo}, "")
	if code != 0 || stdout != "" || stderr != tooOldLine()+"\n" {
		t.Fatalf("commitmsg = (%d, %q, %q), want (0, \"\", %q)", code, stdout, stderr, tooOldLine()+"\n")
	}
}

// The same hook, with a repo this binary meets: it judges, so the line above
// is the guard's doing and not a hook that never refused anything.
func TestCompatGuard_TheSameHookJudgesWhenTheBinaryMeetsTheMinimum(t *testing.T) {
	repo := requiringRepo(t, ">=1.0")
	msg := commitMessageIn(t, repo)

	code, _, stderr := runCLI([]string{"gate", "commitmsg", msg, "--repo", repo}, "")
	if code != 1 || !strings.Contains(stderr, "Co-Authored-By") {
		t.Fatalf("commitmsg = (%d, %q), want the attribution refused with exit 1", code, stderr)
	}
}

func TestCompatGuard_AClaudeEditHookFailsOpenInATooOldRepo(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	payload := `{"tool_name":"Write","cwd":` + jsonString(t, repo) + `,"tool_input":{"file_path":` + jsonString(t, filepath.Join(repo, "a_test.go")) + `,"content":"assert x == x"}}`

	code, stdout, stderr := runCLI([]string{"gate", "pretooluse"}, payload)
	if code != 0 || stdout != "" || stderr != tooOldLine()+"\n" {
		t.Fatalf("pretooluse = (%d, %q, %q), want (0, \"\", %q)", code, stdout, stderr, tooOldLine()+"\n")
	}
}

// The control for the test above, and the proof the guard hands the payload on
// intact: with the minimum met the very same payload is refused by the hook.
func TestCompatGuard_TheSameEditIsJudgedWhenTheBinaryMeetsTheMinimum(t *testing.T) {
	repo := requiringRepo(t, ">=1.0")
	payload := `{"tool_name":"Write","cwd":` + jsonString(t, repo) + `,"tool_input":{"file_path":` + jsonString(t, filepath.Join(repo, "a_test.go")) + `,"content":"assert x == x"}}`

	code, stdout, _ := runCLI([]string{"gate", "pretooluse"}, payload)
	if code != 2 || !strings.Contains(stdout, `"decision":"block"`) {
		t.Fatalf("pretooluse = (%d, %q), want the tautology blocked with exit 2", code, stdout)
	}
}

// The edit's own file decides which repo is judged, not the directory the
// session happens to sit in.
func TestCompatGuard_TheEditedFilesRepoDecidesNotTheSessionsDirectory(t *testing.T) {
	tooOld := requiringRepo(t, ">=99.0")
	elsewhere := t.TempDir()
	// cwd is the too-old repo, the file is in a directory no repo owns: the hook runs.
	payload := `{"tool_name":"Write","cwd":` + jsonString(t, tooOld) + `,"tool_input":{"file_path":` + jsonString(t, filepath.Join(elsewhere, "a_test.go")) + `,"content":"assert x == x"}}`
	if code, _, stderr := runCLI([]string{"gate", "pretooluse"}, payload); code != 2 || stderr != "" {
		t.Fatalf("file outside the repo: pretooluse = (%d, %q), want it judged and blocked", code, stderr)
	}
	// cwd is no repo, the file is in the too-old one: the hook stands down.
	payload = `{"tool_name":"Write","cwd":` + jsonString(t, elsewhere) + `,"tool_input":{"file_path":` + jsonString(t, filepath.Join(tooOld, "a_test.go")) + `,"content":"assert x == x"}}`
	if code, _, stderr := runCLI([]string{"gate", "pretooluse"}, payload); code != 0 || stderr != tooOldLine()+"\n" {
		t.Fatalf("file in the too-old repo: pretooluse = (%d, %q), want fail open with the line", code, stderr)
	}
}

// A session start is the one hook whose output reaches the model, so it carries
// the line there and keeps stderr, which nobody reads, empty.
func TestCompatGuard_SessionStartCarriesTheLineAsContext(t *testing.T) {
	gateConfigDir(t)
	repo := requiringRepo(t, ">=99.0")

	code, stdout, stderr := runCLI([]string{"gate", "sessionstart"}, `{"session_id":"s","cwd":`+jsonString(t, repo)+`}`)
	if code != 0 || stderr != "" {
		t.Fatalf("sessionstart = (%d, stderr %q), want (0, empty)", code, stderr)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout %q is not the SessionStart envelope: %v", stdout, err)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" || out.HookSpecificOutput.AdditionalContext != tooOldLine() {
		t.Fatalf("envelope = %+v, want SessionStart carrying %q", out.HookSpecificOutput, tooOldLine())
	}
}

func TestCompatGuard_EveryGitHookAndEveryClaudeHookStandsDownInATooOldRepo(t *testing.T) {
	gateConfigDir(t)
	repo := requiringRepo(t, ">=99.0")
	t.Chdir(repo)
	payload := `{"tool_name":"Bash","cwd":` + jsonString(t, repo) + `,"tool_input":{"command":"ls"}}`
	for _, sub := range []string{"sessionstart", "pretooluse", "posttooluse", "userpromptsubmit", "sessionend", "precommit", "premerge", "premergecommit", "prepush", "postcommit", "postmerge"} {
		for _, name := range []string{"gate", "tdd"} {
			code, _, _ := runCLI([]string{name, sub}, payload)
			if code != 0 {
				t.Errorf("%s %s exit = %d, want 0: a hook never fails a repo it cannot read", name, sub, code)
			}
		}
	}
}

func TestCompatGuard_AVerbThatWritesRepoStateRefusesWithTheSameLine(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	t.Chdir(repo)
	for _, args := range [][]string{
		{"ratchet", "check"},
		{"ratchet", "test"},
		{"check"},
		{"docs", "check"},
		{"sqlc", "regen"},
		{"install"},
		{"gate", "init"},
		{"gate", "install"},
		{"gate", "split-commit"},
		{"gate", "probe", "discard", "x.go"},
		{"gate", "escape", "record", "why"},
		{"gate", "mutants", "run"},
		{"workspace", "commit", "-m", "x"},
		{"workspace", "merge"},
		{"workspace", "ship"},
		{"workspace", "prune"},
	} {
		code, stdout, stderr := runCLI(args, "")
		if code != 1 || stdout != "" || stderr != tooOldLine()+"\n" {
			t.Errorf("%v = (%d, %q, %q), want (1, \"\", %q)", args, code, stdout, stderr, tooOldLine()+"\n")
		}
	}
}

func TestCompatGuard_ARefusedVerbWritesNothingIntoTheRepo(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	t.Chdir(repo)
	if code, _, _ := runCLI([]string{"ratchet", "init"}, ""); code != 1 {
		t.Fatalf("ratchet init exit = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(repo, ".ratchet")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ratchet init left .ratchet behind in a repo it refused (stat err: %v)", err)
	}
}

func TestCompatGuard_TheRepoFlagNamesTheRepoTheVerbActsOn(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	t.Chdir(t.TempDir()) // a directory no repo owns
	for _, args := range [][]string{
		{"ratchet", "check", "--repo", repo},
		{"ratchet", "check", "--repo=" + repo},
		{"ratchet", "check", "-repo", repo},
	} {
		if code, _, stderr := runCLI(args, ""); code != 1 || stderr != tooOldLine()+"\n" {
			t.Errorf("%v = (%d, %q), want a refusal naming the repo's minimum", args, code, stderr)
		}
	}
}

func TestCompatGuard_VerbsThatReadNoRepoFormatRunInATooOldRepo(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	t.Chdir(repo)

	code, stdout, stderr := runCLI([]string{"version"}, "")
	if code != 0 || !strings.HasPrefix(stdout, "aphrollo "+buildinfo.Version()) || stderr != "" {
		t.Fatalf("version = (%d, %q, %q), want it to print itself", code, stdout, stderr)
	}
	// update is the cure the line names, so it can never be what is refused.
	if _, _, stderr := runCLI([]string{"update", "--help"}, ""); strings.Contains(stderr, "too old") {
		t.Fatalf("update refused itself: %q", stderr)
	}
	// A hook that reads a command, not the repo, has no format to misread.
	code, _, stderr = runCLI([]string{"guardrail", "pretooluse"}, `{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if code != 0 || stderr != "" {
		t.Fatalf("guardrail pretooluse = (%d, %q), want it silent and allowing", code, stderr)
	}
}

func TestCompatGuard_AskingForHelpIsNeverRefused(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	t.Chdir(repo)
	for _, args := range [][]string{{"ratchet", "-h"}, {"ratchet", "check", "--help"}, {"workspace", "help"}, {"gate", "init", "-h"}} {
		if _, _, stderr := runCLI(args, ""); strings.Contains(stderr, "too old") {
			t.Errorf("%v was refused for a repo's minimum: %q", args, stderr)
		}
	}
}

func TestCompatGuard_AMalformedRequiresRefusesAVerbAndFailsAHookOpen(t *testing.T) {
	repo := requiringRepo(t, "newest")
	t.Chdir(repo)
	want := "aphrollo: aphrollo.toml: requires = \"newest\" is not a version this binary can compare; write the oldest version the repo accepts, like requires = \">=1.4\"\n"

	if code, stdout, stderr := runCLI([]string{"ratchet", "check"}, ""); code != 1 || stdout != "" || stderr != want {
		t.Errorf("ratchet check = (%d, %q, %q), want (1, \"\", %q)", code, stdout, stderr, want)
	}
	msg := commitMessageIn(t, repo)
	if code, stdout, stderr := runCLI([]string{"gate", "commitmsg", msg}, ""); code != 0 || stdout != "" || stderr != want {
		t.Errorf("commitmsg = (%d, %q, %q), want (0, \"\", %q)", code, stdout, stderr, want)
	}
}

func TestCompatGuard_ARepoWithNoRequiresIsNeverTouched(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	msg := filepath.Join(repo, "COMMIT_EDITMSG")
	writeFile(t, msg, attributionTrailer)
	writeFile(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")

	code, _, stderr := runCLI([]string{"gate", "commitmsg", msg}, "")
	if code != 1 || strings.Contains(stderr, "too old") {
		t.Fatalf("commitmsg = (%d, %q), want the hook to judge as it always did", code, stderr)
	}
}

// A hook whose stdin cannot be read still reports it the way it always did:
// the guard read the stream first and must not swallow the failure.
func TestCompatGuard_AnUnreadableHookPayloadStillReportsItsError(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "pretooluse"}, iotest.ErrReader(errors.New("pipe broke")), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "reading hook input: pipe broke") {
		t.Fatalf("pretooluse = (%d, %q), want exit 1 naming the read error", code, errb.String())
	}
}

func TestCompatClassOf_SortsEveryVerbIntoOpenHookOrRefuse(t *testing.T) {
	open, hook, refuse := compatOpen, compatHook, compatRefuse
	cases := []struct {
		args []string
		want compatClass
	}{
		{nil, open},
		{[]string{"-h"}, open},
		{[]string{"--help"}, open},
		{[]string{"help"}, open},
		{[]string{"version"}, open},
		{[]string{"update"}, open},
		{[]string{"release", "plan"}, open},
		{[]string{"changelog"}, open},
		{[]string{"status"}, open},
		{[]string{"dev", "up"}, open},
		{[]string{"guardrail", "pretooluse"}, open},
		{[]string{"refactor"}, open},
		{[]string{"find"}, open},
		{[]string{"outline"}, open},
		{[]string{"show"}, open},
		{[]string{"config"}, open},
		{[]string{"issue"}, open},
		{[]string{"feedback"}, open},
		{[]string{"ci", "why"}, open},
		{[]string{"gate"}, open},
		{[]string{"gate", "allow"}, open},
		{[]string{"gate", "revoke"}, open},
		{[]string{"gate", "primary-edits"}, open},
		{[]string{"gate", "doctor"}, open},
		{[]string{"gate", "statusline"}, open},
		{[]string{"gate", "stats"}, open},
		{[]string{"gate", "output"}, open},
		{[]string{"gate", "status"}, open},
		{[]string{"gate", "classify-diff"}, open},
		{[]string{"gate", "runphase"}, open},
		{[]string{"gate", "selfcheck"}, open},
		{[]string{"gate", "cargo"}, open},
		{[]string{"gate", "git"}, open},
		{[]string{"gate", "lint"}, open},
		{[]string{"gate", "gc"}, open},
		{[]string{"gate", "issue"}, open},
		{[]string{"gate", "feedback"}, open},
		{[]string{"workspace"}, open},
		{[]string{"workspace", "list"}, open},
		{[]string{"tdd", "stats"}, open},

		{[]string{"gate", "sessionstart"}, hook},
		{[]string{"gate", "pretooluse"}, hook},
		{[]string{"gate", "posttooluse"}, hook},
		{[]string{"gate", "userpromptsubmit"}, hook},
		{[]string{"gate", "sessionend"}, hook},
		{[]string{"gate", "precommit"}, hook},
		{[]string{"gate", "premerge"}, hook},
		{[]string{"gate", "premergecommit"}, hook},
		{[]string{"gate", "prepush"}, hook},
		{[]string{"gate", "commitmsg", "msg"}, hook},
		{[]string{"gate", "postcommit"}, hook},
		{[]string{"gate", "postmerge"}, hook},
		{[]string{"tdd", "precommit"}, hook},

		{[]string{"ratchet"}, refuse},
		{[]string{"ratchet", "check"}, refuse},
		{[]string{"ratchet", "test"}, refuse},
		{[]string{"ratchet", "init"}, refuse},
		{[]string{"check"}, refuse},
		{[]string{"docs", "check"}, refuse},
		{[]string{"sqlc", "check"}, refuse},
		{[]string{"sqlc", "regen"}, refuse},
		{[]string{"install"}, refuse},
		{[]string{"gate", "init"}, refuse},
		{[]string{"gate", "install"}, refuse},
		{[]string{"gate", "split-commit"}, refuse},
		{[]string{"gate", "probe"}, refuse},
		{[]string{"gate", "escape"}, refuse},
		{[]string{"gate", "mutants"}, refuse},
		{[]string{"gate", "a-verb-added-later"}, refuse},
		{[]string{"workspace", "commit"}, refuse},
		{[]string{"workspace", "merge"}, refuse},
		{[]string{"workspace", "create"}, refuse},
		{[]string{"a-verb-added-later"}, refuse},
		{[]string{"tdd", "init"}, refuse},

		{[]string{"ratchet", "-h"}, open},
		{[]string{"ratchet", "--help"}, open},
		{[]string{"ratchet", "check", "-h"}, open},
		{[]string{"workspace", "help"}, open},
		{[]string{"gate", "init", "--help"}, open},
		{[]string{"gate", "precommit", "--help"}, open},
	}
	for _, c := range cases {
		if got := compatClassOf(c.args); got != c.want {
			t.Errorf("compatClassOf(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestCompatHookDir_NamesTheDirectoryAHookPayloadIsAbout(t *testing.T) {
	work := t.TempDir()
	repoDir := filepath.Join(work, "repo")
	otherDir := filepath.Join(work, "other")
	absFile := filepath.Join(repoDir, "src", "a.go")
	payload := func(cwd, file string) string {
		return `{"cwd":` + jsonString(t, cwd) + `,"tool_input":{"file_path":` + jsonString(t, file) + `}}`
	}
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"absolute file", payload(otherDir, absFile), filepath.Join(repoDir, "src")},
		{"relative file under cwd", payload(repoDir, filepath.Join("src", "a.go")), filepath.Join(repoDir, "src")},
		{"no file, a cwd", `{"cwd":` + jsonString(t, repoDir) + `,"tool_input":{"command":"ls"}}`, repoDir},
		{"nothing to go on", `{"tool_input":{}}`, "."},
		{"not json", `{bad`, "."},
		{"empty", ``, "."},
	}
	for _, c := range cases {
		if got := compatHookDir([]byte(c.raw)); got != c.want {
			t.Errorf("%s: compatHookDir = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCompatRepoFlag_ReadsTheRepoAVerbWasPointedAt(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"ratchet", "check", "--repo", "/r"}, "/r"},
		{[]string{"ratchet", "check", "-repo", "/r"}, "/r"},
		{[]string{"ratchet", "check", "--repo=/r"}, "/r"},
		{[]string{"ratchet", "check", "-repo=/r"}, "/r"},
		{[]string{"ratchet", "check", "--repo", "/a", "--repo", "/b"}, "/b"},
		{[]string{"ratchet", "check"}, "."},
		{[]string{"ratchet", "check", "--repo"}, "."},
		{[]string{"ratchet", "check", "--repository", "/r"}, "."},
		{[]string{"workspace", "commit", "-m", "--repo"}, "."},
	}
	for _, c := range cases {
		if got := compatRepoFlag(c.args); got != c.want {
			t.Errorf("compatRepoFlag(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A binary built at no release tag has no version to compare with a repo's
// minimum. It does not stand down (that is for a binary known to be too old):
// the hook judges as ever, and one line on stderr says the minimum went
// unchecked.
func TestCompatGuard_ADevBuildJudgesATooOldRepoAndSaysItDidNotCheckTheMinimum(t *testing.T) {
	repo := requiringRepo(t, ">=99.0")
	buildinfo.SetVersionForTest("")
	msg := commitMessageIn(t, repo)

	code, _, stderr := runCLI([]string{"gate", "commitmsg", msg, "--repo", repo}, "")
	notice := "aphrollo: dev build " + buildinfo.Version() + " is not at a release tag, so this repo's requires >=99.0 is not checked\n"
	if code != 1 || !strings.Contains(stderr, "Co-Authored-By") || !strings.Contains(stderr, notice) {
		t.Fatalf("commitmsg = (%d, %q), want the attribution refused with exit 1 and the line %q", code, stderr, notice)
	}
}
