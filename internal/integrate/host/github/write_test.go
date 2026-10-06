package github

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// A write cannot be recorded against a real repository without changing it, so
// these are tested on the request: the argv gh is given, and what the adapter
// makes of the answers gh is documented to give.

type call struct {
	timeout time.Duration
	args    []string
}

// scripted answers each call by its arguments and records them.
type scripted struct {
	t     *testing.T
	calls []call
	reply func(args []string) ([]byte, error)
}

func (s *scripted) run(_ string, timeout time.Duration, args ...string) ([]byte, error) {
	s.calls = append(s.calls, call{timeout, args})
	return s.reply(args)
}

func (s *scripted) host(origin string) *GitHub {
	return New(Options{Dir: "/lane", Runner: s.run, Origin: func() string { return origin }})
}

const originURL = "git@github.com:acme/widgets.git"

func has(args []string, want ...string) bool {
	for i := range args {
		if i+len(want) <= len(args) && slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestEnqueue_BindsTheMutationToTheJudgedHeadAndNamesTheBaseRepo(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if has(args, "--jq", ".node_id") {
			return []byte("PR_kwDOabc\n"), nil
		}
		return []byte(`{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"E","position":1}}}}`), nil
	}

	err := s.host(originURL).Enqueue("upstream/gadgets", 7, "abc1234")

	if err != nil {
		t.Fatal(err)
	}
	if len(s.calls) != 2 || !has(s.calls[0].args, "repos/upstream/gadgets/pulls/7") {
		t.Fatalf("calls = %v, want the node id of the BASE repo's PR first", s.calls)
	}
	mutation := s.calls[1].args
	if !has(mutation, "-f", "id=PR_kwDOabc") || !has(mutation, "-f", "sha=abc1234") || !strings.Contains(strings.Join(mutation, " "), "expectedHeadOid:$sha") {
		t.Errorf("mutation argv = %v, want the node id and the head as expectedHeadOid", mutation)
	}
}

func TestEnqueue_AGraphQLErrorBodyOnExitZeroIsARefusal(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if has(args, "--jq", ".node_id") {
			return []byte("PR_x"), nil
		}
		return []byte(`{"errors":[{"message":"nope"}]}`), nil
	}

	err := s.host(originURL).Enqueue("", 7, "abc")

	if err == nil || !strings.Contains(err.Error(), "enqueue PR #7") || !strings.Contains(err.Error(), "GitHub refused the enqueue") {
		t.Fatalf("err = %v, want the refusal with GitHub's body", err)
	}
}

func TestEnqueue_AHeadThatMovedIsTheHeadMovedRefusalNotAGenericOne(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if has(args, "--jq", ".node_id") {
			return []byte("PR_x"), nil
		}
		return []byte(`{"errors":[{"message":"Head branch was modified. Review the changes and try again."}]}`), nil
	}

	err := s.host(originURL).Enqueue("", 7, "abc1234567")

	var moved *host.HeadMovedError
	if !errors.As(err, &moved) || !strings.Contains(err.Error(), "merge bound to abc1234") {
		t.Fatalf("err = %v, want the head-moved refusal", err)
	}
}

func TestMerge_ARESTMergeIsBoundToTheHeadAndGetsTheVerbsOwnSubject(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		switch {
		case has(args, "--jq", ".[0].number // empty"):
			return []byte("7\n"), nil
		case has(args, "--jq", ".title"):
			return []byte("Add the thing\n"), nil
		}
		return []byte("{}"), nil
	}

	err := s.host(originURL).Merge(host.MergeRequest{Branch: "lane/x", Method: "squash", Head: "abc1234"})

	if err != nil {
		t.Fatal(err)
	}
	put := s.calls[len(s.calls)-1].args
	for _, want := range [][]string{{"-X", "PUT"}, {"-f", "merge_method=squash"}, {"-f", "sha=abc1234"}, {"-f", "commit_title=Add the thing (#7)"}} {
		if !has(put, want...) {
			t.Errorf("merge argv = %v, want %v", put, want)
		}
	}
	if !has(put, "repos/acme/widgets/pulls/7/merge") {
		t.Errorf("merge argv = %v, want the PR's merge route", put)
	}
}

func TestMerge_ARebaseWritesNoCommitTitleAndNeverReadsTheTitle(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if has(args, "--jq", ".[0].number // empty") {
			return []byte("7"), nil
		}
		return []byte("{}"), nil
	}

	if err := s.host(originURL).Merge(host.MergeRequest{Branch: "lane/x", Method: "rebase", Head: "abc"}); err != nil {
		t.Fatal(err)
	}

	for _, c := range s.calls {
		if has(c.args, "--jq", ".title") || strings.Contains(strings.Join(c.args, " "), "commit_title") {
			t.Errorf("a rebase read or wrote a title: %v", c.args)
		}
	}
}

func TestMerge_ACarriedBodyGoesThroughGhPrMergeBoundToTheHead(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return nil, nil }

	err := s.host(originURL).Merge(host.MergeRequest{Branch: "lane/x", Method: "merge", Head: "abc", Subject: "S (#7)", Body: "B", UseBody: true})

	if err != nil || len(s.calls) != 1 {
		t.Fatalf("calls = %v, err = %v; want the one gh pr merge", s.calls, err)
	}
	want := MergeBodyArgs("merge", "S (#7)", "B", "abc", "lane/x")
	if !slices.Equal(s.calls[0].args, want) || !has(want, "--match-head-commit", "abc") {
		t.Errorf("argv = %v, want %v", s.calls[0].args, want)
	}
}

func TestMerge_AMergeRefusedForAMovedHeadIsHeadMovedWhicheverRouteMadeIt(t *testing.T) {
	for name, req := range map[string]host.MergeRequest{
		"api": {Branch: "lane/x", Method: "squash", Head: "abc1234"},
		"cli": {Branch: "lane/x", Method: "squash", Head: "abc1234", Subject: "S", Body: "B", UseBody: true},
	} {
		s := &scripted{t: t}
		s.reply = func(args []string) ([]byte, error) {
			switch {
			case has(args, "--jq", ".[0].number // empty"):
				return []byte("7"), nil
			case has(args, "--jq", ".title"):
				return []byte("T"), nil
			}
			return []byte("GraphQL: Head branch was modified. Review the changes and try again."), errors.New("exit status 1")
		}

		err := s.host(originURL).Merge(req)

		var moved *host.HeadMovedError
		if !errors.As(err, &moved) {
			t.Errorf("%s: err = %v, want the head-moved refusal", name, err)
		}
	}
}

func TestMerge_AnyOtherRefusalCarriesGitHubsOwnWords(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if has(args, "--jq", ".[0].number // empty") {
			return []byte("7"), nil
		}
		if has(args, "--jq", ".title") {
			return []byte("T"), nil
		}
		return []byte("Pull Request is not mergeable"), errors.New("exit status 1")
	}

	err := s.host(originURL).Merge(host.MergeRequest{Branch: "lane/x", Method: "squash", Head: "abc"})

	if err == nil || !strings.Contains(err.Error(), "gh api pulls merge") || !strings.Contains(err.Error(), "Pull Request is not mergeable") {
		t.Fatalf("err = %v, want gh's own words", err)
	}
}

func TestEditBody_PatchesThePullRequestFoundForTheBranch(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if has(args, "--jq", ".[0].number // empty") {
			return []byte("7"), nil
		}
		return []byte("{}"), nil
	}

	if err := s.host(originURL).EditBody("lane/x", "new body"); err != nil {
		t.Fatal(err)
	}

	patch := s.calls[len(s.calls)-1].args
	if !has(patch, "-X", "PATCH") || !has(patch, "-f", "body=new body") || !has(patch, "repos/acme/widgets/pulls/7") {
		t.Errorf("argv = %v, want a PATCH of the body of #7", patch)
	}
}

func TestEditBody_ABranchWithNoPullRequestIsRefusedByName(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return []byte(""), nil }

	err := s.host(originURL).EditBody("lane/x", "b")

	if err == nil || !strings.Contains(err.Error(), "no PR found for lane/x") {
		t.Fatalf("err = %v, want the missing PR named", err)
	}
}

func TestMarkReady_UsesGhsOwnReadyAndFallsBackOnlyWhenGraphQLIsBlocked(t *testing.T) {
	blocked := &scripted{t: t}
	blocked.reply = func(args []string) ([]byte, error) {
		switch {
		case args[0] == "pr" && args[1] == "ready":
			return []byte("HTTP 403: GitHub GraphQL is not available from Claude Code sessions; use the REST API"), errors.New("exit status 1")
		case has(args, "--jq", ".[0].number // empty"):
			return []byte("7"), nil
		}
		return []byte("{}"), nil
	}
	if err := blocked.host(originURL).MarkReady("lane/x"); err != nil {
		t.Fatalf("blocked GraphQL: %v", err)
	}
	if last := blocked.calls[len(blocked.calls)-1].args; !has(last, "-X", "POST") || !has(last, "repos/acme/widgets/pulls/7/ccr/ready_for_review") {
		t.Errorf("fallback argv = %v, want the sandbox REST route", last)
	}

	other := &scripted{t: t}
	other.reply = func([]string) ([]byte, error) {
		return []byte("HTTP 401: Bad credentials"), errors.New("exit status 1")
	}
	err := other.host(originURL).MarkReady("lane/x")
	if err == nil || !strings.Contains(err.Error(), "Bad credentials") || len(other.calls) != 1 {
		t.Errorf("another failure = %v after %d calls, want it as it is with no fallback tried", err, len(other.calls))
	}
}

func TestOpenPR_PostsTheRequestAndAnswersTheNewPR(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) {
		return []byte(`{"number":9,"html_url":"https://github.com/acme/widgets/pull/9","state":"open","draft":true,"head":{"ref":"lane/x","sha":"abc"},"base":{"ref":"main","repo":{"full_name":"acme/widgets"}}}`), nil
	}

	pr, err := s.host(originURL).OpenPR(host.OpenRequest{Base: "main", Branch: "lane/x", Title: "T", Body: "B", Draft: true})

	if err != nil || pr.Number != 9 || !pr.IsDraft || pr.State != "OPEN" || pr.BaseRepo != "acme/widgets" {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
	args := s.calls[0].args
	for _, want := range [][]string{{"-f", "base=main"}, {"-f", "head=lane/x"}, {"-f", "title=T"}, {"-f", "body=B"}, {"-F", "draft=true"}} {
		if !has(args, want...) {
			t.Errorf("argv = %v, want %v", args, want)
		}
	}
}

func TestDequeue_NamesTheEntryAndAFailureCarriesGitHubsWords(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return []byte("no such entry"), errors.New("exit status 1") }

	err := s.host(originURL).Dequeue("MQE_1")

	if err == nil || !strings.Contains(err.Error(), "dequeue MQE_1") || !strings.Contains(err.Error(), "no such entry") || !has(s.calls[0].args, "-f", "id=MQE_1") {
		t.Fatalf("err = %v, argv = %v", err, s.calls[0].args)
	}
}

func TestRulesReadResult_A404MeansNoRulesetsAndAnythingElseIsAnErrorWithTheFix(t *testing.T) {
	out, err := RulesReadResult("main", []byte("gh: Not Found (HTTP 404)"), errors.New("exit status 1"))
	if err != nil || string(out) != "[]" {
		t.Errorf("404 = %q, %v; want an empty rule list", out, err)
	}
	_, err = RulesReadResult("main", []byte("HTTP 502"), errors.New("exit status 1"))
	if err == nil || !strings.Contains(err.Error(), "gh auth status") {
		t.Errorf("502 = %v, want the fix named", err)
	}
}

func TestRulesHaveMergeQueue_ReadsOneListAndPagesBackToBack(t *testing.T) {
	for in, want := range map[string]bool{
		`[]`:                                     false,
		`[{"type":"pull_request"}]`:              false,
		`[{"type":"merge_queue"}]`:               true,
		`[{"type":"x"}][{"type":"merge_queue"}]`: true,
	} {
		got, err := RulesHaveMergeQueue([]byte(in))
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := RulesHaveMergeQueue([]byte(`[{`)); err == nil {
		t.Error("a torn page parsed")
	}
}

func TestOpenIssue_RoutesToTheNamedTrackerWithItsLabelsAndReadsTheURL(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) {
		return []byte("Creating issue in acme/widgets\n\nhttps://github.com/acme/widgets/issues/12\n"), nil
	}

	url, err := s.host(originURL).OpenIssue(host.IssueRequest{Title: "T", Body: "B", Repo: "acme/widgets", Labels: []string{"escape", "quality"}})

	if err != nil || url != "https://github.com/acme/widgets/issues/12" {
		t.Fatalf("url = %q, %v", url, err)
	}
	args := s.calls[0].args
	if !slices.Equal(args, []string{"issue", "create", "--title", "T", "--body", "B", "--repo", "acme/widgets", "--label", "escape", "--label", "quality"}) {
		t.Errorf("argv = %v", args)
	}
}

func TestOpenIssue_AnAnswerWithNoIssueURLIsNotSuccess(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return []byte("something else\n"), nil }

	_, err := s.host(originURL).OpenIssue(host.IssueRequest{Title: "T"})

	if err == nil || !strings.Contains(err.Error(), "printed no issue URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureLabel_CreatesWithForceColourAndDescription(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return nil, nil }

	if err := s.host(originURL).EnsureLabel("escape", "ff0000", "a miss"); err != nil {
		t.Fatal(err)
	}

	if want := []string{"label", "create", "escape", "--force", "--color", "ff0000", "--description", "a miss"}; !slices.Equal(s.calls[0].args, want) {
		t.Errorf("argv = %v, want %v", s.calls[0].args, want)
	}
}

func TestWithin_BoundsEachCallToTheGivenTimeAndDefaultsToSixtySeconds(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return []byte("[]"), nil }
	g := s.host(originURL)

	_, _, _ = g.FindPR("b")
	_, _, _ = g.Within(5 * time.Second).(*GitHub).FindPR("b")

	if s.calls[0].timeout != DefaultTimeout || s.calls[1].timeout != 5*time.Second {
		t.Errorf("timeouts = %v, %v; want 60s then 5s", s.calls[0].timeout, s.calls[1].timeout)
	}
}

func TestOwnerRepo_ARemoteThatIsNotGitHubIsRefusedNamingTheDirectory(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return nil, nil }

	_, _, err := s.host("https://gitlab.com/acme/widgets.git").FindPR("b")

	if err == nil || !strings.Contains(err.Error(), "origin is not a github remote in /lane") {
		t.Fatalf("err = %v", err)
	}
	if len(s.calls) != 0 {
		t.Error("gh was asked about a repository that is not on GitHub")
	}
}

func TestNormalizeURL_ReducesEachRemoteShapeToItsWebBase(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/widgets.git":          "https://github.com/acme/widgets",
		"https://github.com/acme/widgets.git":      "https://github.com/acme/widgets",
		"https://user:tok@github.com/acme/widgets": "https://github.com/acme/widgets",
		"ssh://git@github.com/acme/widgets.git":    "https://github.com/acme/widgets",
		"https://gitlab.com/acme/widgets.git":      "",
		"file:///srv/git/widgets.git":              "",
		"":                                         "",
	} {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloseIssue_CommentsThenClosesTheNumberedIssueAndFailureCarriesGitHubsWords(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return nil, nil }
	if err := s.host(originURL).CloseIssue(12, "Superseded by #13"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"issue", "close", "12", "--comment", "Superseded by #13"}; !slices.Equal(s.calls[0].args, want) {
		t.Errorf("argv = %v, want %v", s.calls[0].args, want)
	}
	s.reply = func([]string) ([]byte, error) { return []byte("no such issue"), errors.New("exit 1") }
	if err := s.host(originURL).CloseIssue(12, ""); err == nil || !strings.Contains(err.Error(), "no such issue") {
		t.Errorf("err = %v, want gh's own words", err)
	}
	if want := []string{"issue", "close", "12"}; !slices.Equal(s.calls[1].args, want) {
		t.Errorf("no comment: argv = %v, want %v", s.calls[1].args, want)
	}
}

func TestWhoami_AsksGhForTheLoginAndListIssuesCarriesTheAuthor(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if args[0] == "api" {
			return []byte("octo\n"), nil
		}
		return []byte(`[{"number":3,"title":"t","state":"OPEN","author":{"login":"octo"},"labels":[{"name":"report"}]}]`), nil
	}
	who, err := s.host(originURL).Whoami()
	if err != nil || who != "octo" {
		t.Fatalf("Whoami = %q, %v", who, err)
	}
	if want := []string{"api", "user", "--jq", ".login"}; !slices.Equal(s.calls[0].args, want) {
		t.Errorf("argv = %v, want %v", s.calls[0].args, want)
	}
	got, err := s.host(originURL).ListIssues(host.IssueQuery{State: "all", Fields: []string{"number", "author", "labels"}})
	if err != nil || len(got) != 1 || got[0].Author != "octo" || !slices.Equal(got[0].Labels, []string{"report"}) {
		t.Errorf("issues = %+v, %v", got, err)
	}
}
