package workspace

import (
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
)

// Every call this package makes to the code host goes through the host port
// (internal/integrate/host): the verbs here decide what to ask, the adapter
// knows how GitHub is asked. The seams below (ghViewPR, ghMergePR, and the
// rest) stay package variables so a test can drive a verb without a host; their
// real bodies are thin readings of the port.

// newHost is the host for the repository dir sits in: GitHub over gh, bounded
// per call by ghTimeout. A seam so a test can hand every verb a host of its own.
var newHost = func(dir string) host.Host {
	return github.New(github.Options{
		Dir:     dir,
		Origin:  func() string { return wtRemoteURL(dir, "origin") },
		Timeout: ghTimeout,
	})
}

func hostFor(dir string) host.Host { return newHost(dir) }

// The port's types are the types this package reads and prints.
type (
	// CheckRun is one check (or legacy commit status) on one commit.
	CheckRun = host.Check
	// QueueEntry is where a queued PR stands in its merge queue.
	QueueEntry = host.QueueEntry
	// QueueRemoval is what the PR's timeline says about leaving the merge queue.
	QueueRemoval = host.QueueRemoval
	// JudgedHeadError is a landing refused because the PR's head is not the one judged.
	JudgedHeadError = host.HeadMovedError
	prRun           = host.PRRun
	runInfo         = host.RunInfo
)

// prInfoOf is a host PR as the verbs here carry it.
func prInfoOf(p *host.PR) *PRInfo {
	if p == nil {
		return nil
	}
	return &PRInfo{
		Number: p.Number, URL: p.URL, State: p.State, IsDraft: p.IsDraft,
		Mergeable: p.Mergeable, MergeStateStatus: p.MergeStateStatus,
		HeadSHA: p.HeadSHA, BaseRef: p.BaseRef, BaseRepo: p.BaseRepo,
	}
}
