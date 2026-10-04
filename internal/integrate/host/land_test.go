package host

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	judged = "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	moved  = "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func landReq() LandRequest {
	return LandRequest{Branch: "lane/x", PR: 7, Base: "main", Head: judged, Method: "squash"}
}

func TestLand_AMergeBoundToTheJudgedHeadWhenTheBaseHasNoQueue(t *testing.T) {
	var got MergeRequest
	f := &Fake{MergeFn: func(r MergeRequest) error { got = r; return nil }}

	landed, err := Land(f, landReq())

	if err != nil || landed.Queued {
		t.Fatalf("Land = %+v, %v; want a merge, not a queue entry", landed, err)
	}
	if got.Head != judged || got.Branch != "lane/x" || got.Method != "squash" {
		t.Errorf("merge request = %+v, want it bound to %s on lane/x", got, judged)
	}
	if slices.Contains(f.Calls(), "Enqueue") {
		t.Errorf("a base with no queue was enqueued into: %v", f.Calls())
	}
}

func TestLand_ACarriedCommitMessageGoesToTheMerge(t *testing.T) {
	var got MergeRequest
	f := &Fake{MergeFn: func(r MergeRequest) error { got = r; return nil }}
	r := landReq()
	r.Subject, r.Body, r.UseBody = "Add the thing (#7)", "what it does", true

	if _, err := Land(f, r); err != nil {
		t.Fatal(err)
	}

	if !got.UseBody || got.Subject != r.Subject || got.Body != r.Body {
		t.Errorf("merge request = %+v, want the subject and body carried", got)
	}
}

func TestLand_AMergeTheHostRefusesComesBackAsTheHostSaidIt(t *testing.T) {
	refusal := HeadMoved(judged)
	f := &Fake{MergeFn: func(MergeRequest) error { return refusal }}

	_, err := Land(f, landReq())

	var moved *HeadMovedError
	if !errors.As(err, &moved) || !strings.Contains(err.Error(), "merge bound to 1111111") {
		t.Fatalf("err = %v, want the head-moved refusal naming the bound commit", err)
	}
}

func TestLand_ARepoWhoseQueueCannotBeReadIsRefusedNotGuessed(t *testing.T) {
	f := &Fake{HasMergeQueueFn: func(string, string) (bool, error) { return false, errors.New("HTTP 500") }}

	_, err := Land(f, landReq())

	if err == nil || !strings.Contains(err.Error(), "reading whether main has a merge queue") {
		t.Fatalf("err = %v, want the unread queue named", err)
	}
	if slices.Contains(f.Calls(), "Merge") {
		t.Error("a PR was merged on a guess about the queue")
	}
}

func queueFake() *Fake {
	return &Fake{
		HasMergeQueueFn: func(string, string) (bool, error) { return true, nil },
		PRByBranchFn:    func(string) (*PR, error) { return &PR{Number: 7, HeadSHA: judged}, nil },
		DequeueHintText: "dequeue it by hand",
	}
}

func TestLand_EnqueuesBoundToTheJudgedHeadAndSaysWhereItStands(t *testing.T) {
	f := queueFake()
	var sha string
	f.EnqueueFn = func(_ string, _ int, s string) error { sha = s; return nil }
	entries := 0
	f.QueueEntryFn = func(string, int) (*QueueEntry, error) {
		entries++
		if entries == 1 {
			return nil, nil // not in the queue before
		}
		return &QueueEntry{ID: "E1", Position: 2, Total: 3, State: "QUEUED"}, nil
	}

	landed, err := Land(f, landReq())

	if err != nil || !landed.Queued || landed.Already {
		t.Fatalf("Land = %+v, %v; want a fresh queue entry", landed, err)
	}
	if sha != judged {
		t.Errorf("enqueued bound to %q, want %q", sha, judged)
	}
	if landed.Entry == nil || landed.Entry.String() != "position 2 of 3 (queued)" {
		t.Errorf("entry = %+v, want position 2 of 3 (queued)", landed.Entry)
	}
	if slices.Contains(f.Calls(), "Merge") {
		t.Error("a PR behind a queue was merged directly")
	}
}

func TestLand_APRAlreadyInTheQueueIsNotEnqueuedAgain(t *testing.T) {
	f := queueFake()
	f.QueueEntryFn = func(string, int) (*QueueEntry, error) { return &QueueEntry{ID: "E1", Position: 1}, nil }

	landed, err := Land(f, landReq())

	if err != nil || !landed.Queued || !landed.Already {
		t.Fatalf("Land = %+v, %v; want the existing entry reported", landed, err)
	}
	if slices.Contains(f.Calls(), "Enqueue") {
		t.Error("a PR already in the queue was enqueued again")
	}
}

func TestLand_APositionTheQueueHasNotSaidYetIsAQueuedPRWithNoEntry(t *testing.T) {
	f := queueFake()

	landed, err := Land(f, landReq())

	if err != nil || !landed.Queued || landed.Entry != nil {
		t.Fatalf("Land = %+v, %v; want queued with no position yet", landed, err)
	}
}

func TestLand_AnEnqueueTheHostRefusesIsReturnedAndNothingIsReadBack(t *testing.T) {
	f := queueFake()
	f.EnqueueFn = func(string, int, string) error { return HeadMoved(judged) }

	_, err := Land(f, landReq())

	var hm *HeadMovedError
	if !errors.As(err, &hm) {
		t.Fatalf("err = %v, want the host's head-moved refusal", err)
	}
	if slices.Contains(f.Calls(), "PRByBranch") {
		t.Error("the head was read back after a refused enqueue")
	}
}

func TestLand_AHeadThatMovedAfterTheEnqueueIsTakenOutOfTheQueue(t *testing.T) {
	f := queueFake()
	f.PRByBranchFn = func(string) (*PR, error) { return &PR{Number: 7, HeadSHA: moved}, nil }
	entries := 0
	f.QueueEntryFn = func(string, int) (*QueueEntry, error) {
		entries++
		if entries == 1 {
			return nil, nil
		}
		return &QueueEntry{ID: "E1", Position: 1}, nil
	}
	var dequeued string
	f.DequeueFn = func(id string) error { dequeued = id; return nil }

	_, err := Land(f, landReq())

	var hm *HeadMovedError
	if !errors.As(err, &hm) || !strings.Contains(err.Error(), "it was taken out of the queue") {
		t.Fatalf("err = %v, want a refusal saying it was taken out", err)
	}
	if dequeued != "E1" {
		t.Errorf("dequeued %q, want E1", dequeued)
	}
	if !strings.Contains(err.Error(), "2222222") || !strings.Contains(err.Error(), "1111111") {
		t.Errorf("err = %v, want both heads named", err)
	}
}

func TestLand_AMovedHeadWhoseDequeueFailsSaysItIsStillQueued(t *testing.T) {
	f := queueFake()
	f.PRByBranchFn = func(string) (*PR, error) { return &PR{Number: 7, HeadSHA: moved}, nil }
	entries := 0
	f.QueueEntryFn = func(string, int) (*QueueEntry, error) {
		entries++
		if entries == 1 {
			return nil, nil
		}
		return &QueueEntry{ID: "E1"}, nil
	}
	f.DequeueFn = func(string) error { return errors.New("HTTP 502") }

	_, err := Land(f, landReq())

	if err == nil || !strings.Contains(err.Error(), "taking it out of the queue failed, so it is STILL QUEUED") || !strings.Contains(err.Error(), "dequeue it by hand") {
		t.Fatalf("err = %v, want it said to be still queued with the hint", err)
	}
}

func TestLand_AMovedHeadWithAnUnreadableEntryIsNotReportedAsDequeued(t *testing.T) {
	f := queueFake()
	f.PRByBranchFn = func(string) (*PR, error) { return &PR{Number: 7, HeadSHA: moved}, nil }
	f.QueueEntryFn = func(string, int) (*QueueEntry, error) { return nil, nil }

	_, err := Land(f, landReq())

	if err == nil || !strings.Contains(err.Error(), "STILL QUEUED at an unjudged head") {
		t.Fatalf("err = %v, want it said to be still queued", err)
	}
	if slices.Contains(f.Calls(), "Dequeue") {
		t.Error("a dequeue was tried with no entry to name")
	}
}

func TestLand_AnUnreadableHeadAfterTheEnqueueIsReportedWithTheHint(t *testing.T) {
	f := queueFake()
	f.PRByBranchFn = func(string) (*PR, error) { return nil, errors.New("HTTP 503") }

	_, err := Land(f, landReq())

	if err == nil || !strings.Contains(err.Error(), "its head could not be read back (HTTP 503)") || !strings.Contains(err.Error(), "dequeue it by hand") {
		t.Fatalf("err = %v, want the unverified head named with the hint", err)
	}
}

func TestQueueEntry_StringNamesPlaceTotalAndState(t *testing.T) {
	for _, c := range []struct {
		e    QueueEntry
		want string
	}{
		{QueueEntry{Position: 1}, "position 1"},
		{QueueEntry{Position: 2, Total: 5}, "position 2 of 5"},
		{QueueEntry{Position: 1, State: "AWAITING_CHECKS"}, "position 1 (awaiting checks)"},
	} {
		if got := c.e.String(); got != c.want {
			t.Errorf("%+v = %q, want %q", c.e, got, c.want)
		}
	}
}
