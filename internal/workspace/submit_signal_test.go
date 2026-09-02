package workspace

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// signalCall records one postSubmitSignal invocation.
type signalCall struct{ base, token, ticketID string }

// stubSignal swaps the postSubmitSignal seam for the test's duration, returning a
// pointer to the recorded calls. status/err are what the fake returns.
func stubSignal(t *testing.T, status int, err error) *[]signalCall {
	t.Helper()
	var calls []signalCall
	o := postSubmitSignal
	postSubmitSignal = func(base, token, ticketID string) (int, error) {
		calls = append(calls, signalCall{base, token, ticketID})
		return status, err
	}
	t.Cleanup(func() { postSubmitSignal = o })
	return &calls
}

// setSignalEnv gives the process the platform-dispatched coder session env so
// raiseSubmitSignal treats it as a tracked ticket.
func setSignalEnv(t *testing.T, base, token, ticketID string) {
	t.Helper()
	t.Setenv(envAPIBase, base)
	t.Setenv(envAPIToken, token)
	t.Setenv(envTicketID, ticketID)
}

// On the FLIP path (draft->ready), submit raises the first-class signal at the
// internal endpoint with the env-carried base/token/ticket, and the receipt says
// it was raised.
func TestSubmit_FlipPathRaisesSignal(t *testing.T) {
	repo := pushedRepo(t)
	setSignalEnv(t, "http://api.local", "v1:tok", "019f234d-a60f-7000-8000-000000000000")
	calls := stubSignal(t, http.StatusNoContent, nil)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("flip path should call the submit endpoint exactly once, got %d", len(*calls))
	}
	got := (*calls)[0]
	if got.base != "http://api.local" || got.token != "v1:tok" || got.ticketID != "019f234d-a60f-7000-8000-000000000000" {
		t.Errorf("signal called with %+v, want the env base/token/ticket", got)
	}
	if !strings.Contains(out.String(), "submit signal: raised") {
		t.Errorf("receipt should report the signal was raised:\n%s", out.String())
	}
}

// On the [skip] path (an ALREADY-READY PR — the post-bounce resubmit case), submit
// STILL raises the signal. This is the whole point: an already-ready PR fires no
// ready_for_review webhook, so the endpoint is the ONLY thing that re-arms review.
func TestSubmit_SkipPathRaisesSignal(t *testing.T) {
	repo := pushedRepo(t)
	setSignalEnv(t, "http://api.local", "v1:tok", "019f234d-a60f-7000-8000-000000000000")
	calls := stubSignal(t, http.StatusNoContent, nil)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: false}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("already-ready PR must not be re-flipped"); return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("[skip] path must still call the submit endpoint (post-bounce re-arm), got %d calls", len(*calls))
	}
	if !strings.Contains(out.String(), "submit signal: raised") {
		t.Errorf("[skip] receipt should report the signal was raised:\n%s", out.String())
	}
}

// On the CREATE path (no PR existed, submit opens one READY outright), submit
// STILL raises the signal. A PR opened ready directly fires only `opened` on
// GitHub's side, never `ready_for_review` — so, like the [skip] path, this
// endpoint call is the ONLY thing that arms review.
func TestSubmit_CreatePathRaisesSignal(t *testing.T) {
	repo := pushedRepo(t)
	setSignalEnv(t, "http://api.local", "v1:tok", "019f234d-a60f-7000-8000-000000000000")
	calls := stubSignal(t, http.StatusNoContent, nil)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			return &PRInfo{Number: 55, URL: "https://github.com/o/r/pull/55", State: "OPEN", IsDraft: req.Draft}, nil
		},
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("a freshly opened ready PR must not be flipped"); return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "pending"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("create path must call the submit endpoint exactly once, got %d", len(*calls))
	}
	if !strings.Contains(out.String(), "submit signal: raised") {
		t.Errorf("create-path receipt should report the signal was raised:\n%s", out.String())
	}
}

// A signal failure (endpoint unreachable) must NOT fail the submit; on the [skip]
// path the warning names the wedge risk.
func TestSubmit_SignalFailureDoesNotFailSubmit(t *testing.T) {
	repo := pushedRepo(t)
	setSignalEnv(t, "http://api.local", "v1:tok", "019f234d-a60f-7000-8000-000000000000")
	stubSignal(t, 0, fmt.Errorf("dial tcp: connection refused"))
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: false}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("a failed submit signal must NOT fail the submit: %v", err)
	}
	o := out.String()
	if !strings.Contains(o, "warning: submit signal not raised") {
		t.Errorf("a signal failure should warn:\n%s", o)
	}
	if !strings.Contains(o, "stuck-watchdog") {
		t.Errorf("the [skip]-path warning should name the wedge risk:\n%s", o)
	}
}

// A 404 from the endpoint is warned-and-continued (the ticket isn't tracked), not
// fatal.
func TestSubmit_Signal404WarnsAndContinues(t *testing.T) {
	repo := pushedRepo(t)
	setSignalEnv(t, "http://api.local", "v1:tok", "019f234d-a60f-7000-8000-000000000000")
	stubSignal(t, http.StatusNotFound, nil)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("a 404 must NOT fail the submit: %v", err)
	}
	if !strings.Contains(out.String(), "ticket not found (404)") {
		t.Errorf("a 404 should be reported and continue:\n%s", out.String())
	}
}

// An operator-local run (no ticket env) skips the signal entirely — no call, no
// failure — and says the webhook bridge covers the flip.
func TestSubmit_NoTicketContextSkipsSignal(t *testing.T) {
	repo := pushedRepo(t)
	t.Setenv(envAPIBase, "")
	t.Setenv(envAPIToken, "")
	t.Setenv(envTicketID, "")
	calls := stubSignal(t, http.StatusNoContent, nil)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return nil })
	stubBody(t, func(wt, branch, body string) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	s, _ := SubmitPlan(targetFor(repo, "feat/y"), "summary")
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("no ticket env must mean no signal call, got %d", len(*calls))
	}
	if !strings.Contains(out.String(), "no ticket context") {
		t.Errorf("receipt should note the signal was skipped:\n%s", out.String())
	}
}

// With no ticket env, the missing-env early-return must branch on the path. On the
// FLIP path (fresh draft->ready) the ready_for_review webhook genuinely covers the
// flip, so the benign line is correct — and it must NOT sound the wedge alarm.
func TestRaiseSubmitSignal_NoEnvFlipPathBenign(t *testing.T) {
	t.Setenv(envAPIBase, "")
	t.Setenv(envAPIToken, "")
	t.Setenv(envTicketID, "")
	var out bytes.Buffer
	raiseSubmitSignal(&out, false)
	got := out.String()
	if !strings.Contains(got, "webhook bridge covers the flip") {
		t.Errorf("flip path with no env should print the benign line:\n%s", got)
	}
	if strings.Contains(got, "wedge") || strings.Contains(got, "SKIPPED") {
		t.Errorf("flip path must NOT sound the wedge alarm:\n%s", got)
	}
}

// With no ticket env, the [skip] path (already-ready PR, every post-bounce
// resubmit) has NO ready_for_review webhook to cover it, so a skipped signal
// wedges the ticket. The receipt must say so loudly — not the benign line.
func TestRaiseSubmitSignal_NoEnvSkipPathLoud(t *testing.T) {
	t.Setenv(envAPIBase, "")
	t.Setenv(envAPIToken, "")
	t.Setenv(envTicketID, "")
	var out bytes.Buffer
	raiseSubmitSignal(&out, true)
	got := out.String()
	if !strings.Contains(got, "SKIPPED") || !strings.Contains(got, "will NOT re-arm") {
		t.Errorf("skip path with no env should sound the wedge alarm:\n%s", got)
	}
	if strings.Contains(got, "webhook bridge covers the flip") {
		t.Errorf("skip path must NOT print the benign flip line:\n%s", got)
	}
}

// Integration-style: the REAL postSubmitSignal seam POSTs to the api's internal
// submit endpoint with the bridge token, at the {ticket-id} path. Simulates a
// post-bounce resubmit reaching a fake endpoint that asserts the request shape.
//
// The base carries a trailing `/api` as supplied by agentsd (AGENTSD_API_BASE),
// the same convention apinotify uses. postSubmitSignal appends the path WITHOUT a
// leading `/api`, so the observed request path is exactly `/api/internal/...` —
// NOT the doubled `/api/api/internal/...` that 404'd platform-wide.
func TestPostSubmitSignal_PostsToInternalEndpoint(t *testing.T) {
	const ticketID = "019f234d-a60f-7000-8000-000000000000"
	cases := []struct {
		name     string
		basePath string // appended to srv.URL to form the base
		wantPath string
	}{
		{
			// The real convention: base ends in `/api` → path is `/api/internal/...`,
			// with NO doubling. This is the exact regression the fix guards.
			name:     "base with trailing /api does not double",
			basePath: "/api",
			wantPath: "/api/internal/tickets/" + ticketID + "/submit",
		},
		{
			// A bare base (no `/api`) appends the path verbatim.
			name:     "bare base appends path verbatim",
			basePath: "",
			wantPath: "/internal/tickets/" + ticketID + "/submit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotToken string
			hit := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit = true
				gotMethod, gotPath, gotToken = r.Method, r.URL.Path, r.Header.Get("X-Bridge-Token")
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			status, err := postSubmitSignal(srv.URL+tc.basePath, "v1:tok", ticketID)
			if err != nil {
				t.Fatalf("postSubmitSignal: %v", err)
			}
			if !hit {
				t.Fatal("the endpoint was never called")
			}
			if status != http.StatusNoContent {
				t.Errorf("status = %d, want 204", status)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %s, want POST", gotMethod)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %s, want %s", gotPath, tc.wantPath)
			}
			if strings.Contains(gotPath, "/api/api/") {
				t.Errorf("path %s doubles /api — the platform-wide 404 regression", gotPath)
			}
			if gotToken != "v1:tok" {
				t.Errorf("X-Bridge-Token = %q, want the bridge token", gotToken)
			}
		})
	}
}
