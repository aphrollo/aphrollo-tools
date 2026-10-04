# Recorded gh responses

`index.json` lists every recorded call: the gh arguments the adapter sent, the
file holding gh's answer (stdout on success, stderr on failure), and the error
if gh exited non-zero. `replay_test.go` answers the adapter from these, keyed by
the exact arguments, so a changed request fails the test until it is recorded
again.

They were recorded on 2026-10-04 by running each read method of the adapter
against this repository (aphrollo/aphrollo-tools) with a runner that wraps
`ExecRunner` and saves what it saw: pull request 1202 (merged through the merge
queue, whose first Pipeline run failed on `test-windows (rest)`), pull request
1204 (open), and the repository's branch rules. A log longer than 12000 bytes is
cut there and says so; JSON is never cut.

The writes (enqueue, merge, edit, ready, open, issue, label, dequeue) change a
repository and are not recorded; `write_test.go` checks the request each sends.

Three answers of the branch-rules read pin the no-queue rule at the port:
`90.out` is a real 404 from this host (a repository that does not exist), and
`91.out` and `92.out` are the 403 answers as GitHub words them, written by hand
because no Free-plan or narrowly scoped token is at hand here: `91.out` is the
text of #1203 (a private repository of a Free organisation), `92.out` a token
without the scope.

## Not recorded

A GraphQL `errors` answer to the enqueue mutation (a refused enqueue, a moved
head) is not here: producing one needs a PR that can be enqueued and moved on
purpose, which this repository's merge queue does not allow without landing
something. `write_test.go` tests that path on hand-written bodies in GitHub's
documented shape (`TestEnqueue_AGraphQLErrorBodyOnExitZeroIsARefusal`,
`TestEnqueue_AHeadThatMovedIsTheHeadMovedRefusalNotAGenericOne`). Replace them
with a recording the first time a real one is captured.
