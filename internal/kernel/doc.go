// Package kernel is the pure decision core of docs/trellis-architecture.md
// (§2): payload → event → Step → one rendered line. It imports the standard
// library and nothing from the box: no os, no os/exec, no clock. Time and every
// fact about the world arrive in the Event; whatever must run in the world
// leaves as an Effect, and what comes of it returns as another event.
//
// This package holds the lane machine only. The TDD machine (§3 "TDD
// machine") and the rule table (§5) are later lanes and extend Event, State
// and Effect; the lane machine tolerates the events they add.
//
// # Where each rule comes from
//
//   - Lifecycle (§3 table): none → open on the first hook in a lane; open →
//     committed on a gated commit; pr on pr.opened; ci_pending, ci_green,
//     ci_red, ci_unavailable as CI reports per declared OS; a new head pushed
//     sends ci_* back to pr; merged on a merge; merged → removed after no actor
//     for 30 minutes with a clean tree and no worktree lock; open or committed
//     with no PR, idle 14 days → abandoned. Each Row in the table names its
//     line.
//   - A merge is a fact (§3, §8 "Escapes"): lane.merged is recorded from every
//     live life, whatever its Source (api, local, ancestry, github), and is
//     never an escape.
//   - A closed lane stays closed (§3): only a lane.opened newer than the close
//     starts the branch again, so a retried or replayed event cannot reopen it.
//   - Idempotence (§3 "Concurrency": results come back as events and a retried
//     transaction is idempotent): every fold is a set or a latest-wins, and
//     effects come only with a change of life, so an event applied twice equals
//     once.
//   - Actors (§3 Actor, §12 F3): "session/agent" strings, last seen per lane.
//     The kernel only counts their activity; it never reads a session id.
//   - Guide, not cage (§1): the machine never refuses anything. Where the
//     document is silent it takes the reading that lets work through: a CI
//     verdict implies a PR, an abandoned lane revives on activity, an event
//     for an unknown lane opens it, an unknown event is ignored.
package kernel
