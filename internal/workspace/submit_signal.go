package workspace

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Env vars a platform-dispatched coder session carries so `submit` can raise the
// first-class readiness signal directly at the api. agentsd exports these into
// the coder child env; an operator-local run has none of them (and needs none —
// there is no ticket workflow to re-arm).
const (
	envAPIBase  = "APHROLLO_API_BASE"  // e.g. http://aphrollo-api.service.consul:8100
	envAPIToken = "APHROLLO_API_TOKEN" // a valid api bridge token (X-Bridge-Token)
	envTicketID = "APHROLLO_TICKET_ID" // the FULL ticket UUID (the branch id is truncated)
)

// submitSignalTimeout bounds the single POST. The endpoint is loopback and the
// signal is thin (no payload), so it returns fast; a hung api must never hold the
// coder's handoff, so the call is short and non-fatal.
const submitSignalTimeout = 5 * time.Second

// postSubmitSignal is the seam over the HTTP POST to the api's internal submit
// endpoint (POST /api/internal/tickets/{id}/submit, loopback + bridge-token
// gated). It returns the response status (0 on a transport error). A package var
// so unit tests assert the call without the network and an integration test
// points it at a fake endpoint.
var postSubmitSignal = func(base, token, ticketID string) (int, error) {
	url := strings.TrimRight(base, "/") + "/api/internal/tickets/" + ticketID + "/submit"
	ctx, cancel := context.WithTimeout(context.Background(), submitSignalTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Bridge-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
	return resp.StatusCode, nil
}

// raiseSubmitSignal fires the authoritative "ready" signal (SignalSubmitted) at
// the api's internal submit endpoint. It runs on ALL THREE submit paths: after a
// legacy draft->ready flip, after opening a fresh PR ready outright, and on the
// already-ready [skip] path.
//
// The endpoint is the FIRST-CLASS readiness trigger. On the flip path the GitHub
// ready_for_review webhook raises the same signal, so the endpoint is redundant
// defense there. On the other two paths — a PR opened ready directly (no draft
// stage) and an already-ready PR (no re-draft) — NO ready_for_review webhook EVER
// fires, so after a sub-threshold bounce (which clears `submitted` and latches
// `resubmitRequired`), THIS call is the ONLY thing that re-arms review. Without
// it the ticket wedges until the stuck-watchdog escalates.
//
// Best-effort by contract: any failure (endpoint unreachable, non-204, 404) is
// warned and NEVER fails the submit — the coder's handoff already succeeded and,
// on the flip path, the webhook bridge still covers it. noWebhookPath selects the
// severity on BOTH the failure path (warnSubmitSignal) AND the missing-env
// early-return: on a path with no ready_for_review webhook a miss (no env or a
// failed POST) is the actual wedge risk and is named as such.
func raiseSubmitSignal(stdout io.Writer, noWebhookPath bool) {
	base, token, ticketID := os.Getenv(envAPIBase), os.Getenv(envAPIToken), os.Getenv(envTicketID)
	if base == "" || token == "" || ticketID == "" {
		// No ticket env. On the flip path this is an operator-local run: the
		// ready_for_review webhook covers the flip, so the skip is benign. On a
		// no-webhook path (freshly opened ready, or an already-ready [skip] resubmit)
		// a skipped signal is the actual wedge: review never re-arms. A platform
		// coder session missing this env there is a plumbing bug, so name it.
		if noWebhookPath {
			fmt.Fprintf(stdout, "  submit signal: SKIPPED with no ticket context — "+
				"review will NOT re-arm; ticket may wedge\n")
			return
		}
		fmt.Fprintf(stdout, "  submit signal: skipped (no ticket context) — webhook bridge covers the flip\n")
		return
	}
	status, err := postSubmitSignal(base, token, ticketID)
	switch {
	case err != nil:
		warnSubmitSignal(stdout, noWebhookPath, fmt.Sprintf("endpoint unreachable (%v)", err))
	case status == http.StatusNoContent:
		fmt.Fprintf(stdout, "  submit signal: raised — review re-armed via the internal endpoint\n")
	case status == http.StatusNotFound:
		fmt.Fprintf(stdout, "  submit signal: ticket not found (404) — continuing (webhook bridge covers the flip)\n")
	default:
		warnSubmitSignal(stdout, noWebhookPath, fmt.Sprintf("endpoint returned %d", status))
	}
}

// warnSubmitSignal prints the non-fatal failure warning. On a no-webhook path it
// names the wedge: no ready_for_review webhook ever fires there, so a missed
// signal leaves review un-re-armed until the stuck-watchdog.
func warnSubmitSignal(stdout io.Writer, noWebhookPath bool, reason string) {
	if noWebhookPath {
		fmt.Fprintf(stdout, "  warning: submit signal not raised (%s) — this PR fires no "+
			"ready_for_review webhook, so review may NOT re-arm until the stuck-watchdog; re-run submit "+
			"or ping the operator\n", reason)
		return
	}
	fmt.Fprintf(stdout, "  warning: submit signal not raised (%s) — the ready_for_review webhook still "+
		"covers this flip\n", reason)
}
