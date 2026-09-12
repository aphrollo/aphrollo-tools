<!-- Written by `aphrollo install`. Edit the aphrollo README's
ratchet-spec section, not this file: the next init overwrites it. -->

### Ratchet laws (`aphrollo ratchet`)

A repo's code laws — "a float `.clamp()` is not a NaN guard", "modules stay
under 600 lines", "every env switch is registered", "every cited `.md` path
resolves" — are **data**, not fifteen hand-written test files each
re-deriving the same scan/baseline/escape machinery. They live in the
consuming repo under `.ratchet/`, and this binary is the engine that runs
them: at **pre-edit** time (before the write lands), at **commit** time, and
by hand.

```
.ratchet/
  laws/<name>.toml          # one rule, declared
  baselines/<name>.txt      # the ceiling it may not exceed (only ever goes down)
  fixtures/<name>/hit/…     # files the law MUST catch, listed in expected.txt
  fixtures/<name>/clean/…   # files it must stay silent on
```

#### The schema

```toml
schema       = 1                               # optional: the law schema this file is written for
name         = "nan-guard"                     # must equal the file stem
description  = "A float clamp is not a NaN guard"
severity     = "deny"                          # deny | warn
escape       = "// nan-safe:"                  # optional: suppresses a hit
escape_lines = 2                               # optional: how far above (default 2)
baseline     = ".ratchet/baselines/nan-guard.txt"   # optional
code_only    = true                            # optional: strip trailing comments first
comment_prefix = "#"                           # optional: what opens one (default "//")
contiguous   = true                            # optional: suppression must be in the comment run above
trigger_exclude = "^\s*(pub )?use "            # optional: lines that can never be a trigger

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**", "crates/ratchet/tests/**"]
ignore_gitignore = false                        # optional: judge gitignored files too
min_files = 40                                  # optional: fewer matched files is a regression
alias = "tier1"                                 # optional: base Include on a named set (below)

[matcher]                                       # exactly ONE
kind    = "regex-absent"
pattern = "\\.clamp\\("
key     = "file:line-content-hash"
```

Parsing is **strict**: an unknown key, a duplicate table, a matcher key that
belongs to another kind, a regex that does not compile, or a `name` that
disagrees with the file it lives in is an error at load. A typo must not
silently disable half a rule.

`schema` is the exception, and only in one direction. It is the version this
law file is written for; absent means `1`, which is every law written before
the key existed. A law declaring a version ABOVE the one the binary supports
is read **leniently** — the keys the binary knows still apply, the ones it has
never heard of are skipped, and it is never a hard error, so a repo whose laws
moved ahead of a box's binary does not wedge that box. It is not silent
either: the run prints one line naming the law and both versions, and the gate
leaves `ratchet-law-newer:<law>` in its log, because a rule read with half its
keys skipped otherwise reports clean exactly like a rule that is being obeyed.
At the supported schema an unknown key stays an error — that is the typo
protection, and it only makes sense where the binary claims to understand the
file. A `schema` that is not a positive integer is a broken law, not a future
one. Scope globbing understands `*`, `?` and `**`,
`exclude` always wins, and the walk is gitignore-aware, so a law never has to
enumerate build output. A repo that ignores a whole extension hides the files
some laws are entirely about (borld ignores `*.md`, which is every doc a
citation law reads) — `ignore_gitignore = true` opts THAT law into the ignored
files, and the fix is never to weaken the repo's `.gitignore` for a guard's
benefit. `.git` is never walked, opt-out or not.

**Scope aliases.** A `scopes.toml` file under `.ratchet/` names reusable file
sets so several laws that share a boundary (Tier-1, presentation, …) state
the glob list once:

```toml
[sets]
tier1 = ["crates/movement/**", "crates/pose/**", "crates/numeric/**"]
```

A law's `[scope].alias` resolves to that set's globs, merged as the BASE of
its own `include` (a law may still widen further with its own entries; both
apply). `ratchet check` refuses a law whose alias names a set `scopes.toml`
never declares — one line naming the law and the alias — and reports, as a
warning, a set no law's alias uses. `ratchet test` resolves an alias the same
way, through the same `LoadLaws` call, so a fixture proves a law through its
alias exactly like it proves one through a literal `include` list.

A scope is a claim about coverage, so the engine judges it too. `min_files`
is the floor below which a clean verdict is not a verdict: a law whose globs
quietly stopped matching (a crate renamed, a `**` dropped) reports green over
files it never opened, and falling under the floor is the finding
`scope-floor`. An `include` entry that names ONE file rather than a set is a
citation: when it is gone the finding is `missing-scope-file | <path>`, which
says something different from "the set is empty". Both are whole-tree
questions, so a narrowed run (`--proposed`, `--files`) does not ask them.

Suppression is bounded by structure, not by arithmetic. `contiguous = true`
makes an `escape` or a `marker` count only when it sits on the trigger's own
line or in the COMMENT RUN directly above it — consecutive comment lines and
single-line attributes, broken by the first code or blank line. Counting lines
instead lets one `// nan-safe:` exempt an unrelated call four lines below,
across code it says nothing about. `comment_prefix` is what opens a comment in
the language being scanned, so a TOML or shell law strips `#` comments and a
commented-out entry stops satisfying a `regex-present` law. `trigger_exclude`
disqualifies a line from ever BEING a trigger, which is what an import needs:
putting `use` in the marker regex instead exempts everything in the window
below the import.

`direction` says WHERE the marker lives: `above` (default) is the
comment-above-the-declaration shape, `below` is a block that carries its own
configuration — a `proptest!` block's `#![proptest_config(…)]` sits on the NEXT
line, and looking up only reported 30 seeded blocks as unseeded — and `both`
accepts either. `contiguous` applies in whichever direction is chosen.

#### Matcher kinds

| kind | keys | the rule | exemplar |
|---|---|---|---|
| `line-count` | `max`, `count`, `unit_split` | a file may not exceed `max` lines; key = file, count = lines (`count = "code"` drops blank and comment-only lines, by the file's own `//`/`/* */`/`#` syntax) | module-size debt |
| `regex-absent` | `pattern`, `key`, `count` | a pattern must NOT appear; `count = "matches"` counts every call on a line, not the line | the bare `.clamp(` guard |
| `path-regex-absent` | `pattern` | the repo-relative PATH must not match; key = the path, no line | a filename carrying a plan-item stamp or a serial letter |
| `regex-present` | `pattern` | every file in scope MUST contain it | a proptest that must carry an explicit seed |
| `marker-within-lines` | `trigger`, `marker`, `lines`, `contiguous`, `direction` | a `trigger` line requires a `marker` on its own line, in its own comment run above (capped at N lines, and stopping at the previous trigger so a marker vouches for exactly one declaration), or — with `direction` — within N lines below | `// bound:` over a collection that grows |
| `registry-both-ways` | `registry_file`, `entry_pattern`, `use_pattern`, `entry_column` | every use is registered AND every registry line is used; the LAST non-empty capture of a use match is the name, so an alternation with one group per branch works; `entry_column` scopes `entry_pattern` to one `\|`-delimited cell of the registry line | the dev-instrument (env switch) registry, and "every crate is documented in this markdown table" |
| `doc-path-resolves` | `pattern` | a captured path must resolve relative to the CITING file's own directory, then the repo root, then inside its own `crates/<x>`/`tools/<x>` unit | doc citations |
| `dep-graph-forbids` | `roots`, `forbidden`, `edges`, `min_reachable` | no root package may REACH a forbidden one (glob) through the resolved dependency graph; `edges = "normal"` (default) never follows dev/build edges, which is the whole distinction | dev-only tooling in a shipping binary |
| `dep-graph-ceiling` | `roots`, `edges`, `counts`, `min_reachable` | how MUCH a root may reach at all: one hit per root, weighted by the count of packages reachable from it, so the baseline ceilings that count the way `json-number-ceiling` ceilings a measured number | a crate whose fan-out across the workspace nobody was watching |
| `file-set-containment` | `superset_file`, `subset_file`, `capture` OR `subset_capture`+`superset_capture` | every capture in `subset_file` must also appear in `superset_file` | a headless stand-in whose query must refuse at least what the real one refuses |
| `json-number-ceiling` | `files`, `path`, `tolerance_pct`, `enabled_env` | a number read out of generated JSON may not exceed its baseline by more than the tolerance | a criterion bench figure nobody was reading |
| `symbol-removed` | `pattern` (exactly one capture group) | a symbol captured at `--base <ref>` must still be captured somewhere in scope at the current tree, or be admitted by a tombstone comment naming it and a reason (or naming the PATH it stood in, once that whole file is gone and nothing from it survives) | a deleted test, invisible to every file-at-a-time law |

The last five judge a whole TREE rather than a file at a time, and each
refuses to reach a VACUOUS verdict: a dependency walk that resolved nothing, a
capture set that came out empty, or an armed perf law with no data all fail
loudly instead of reporting green over files they never opened.

- **`dep-graph-forbids`** runs `cargo metadata --format-version 1` once and
  BFSes `resolve.nodes`, so a TRANSITIVE edge (`server -> helper -> editor`) is
  caught exactly like a direct one. The hit's key is the PATH that reaches the
  forbidden package (`server->shared->testrig`) — that is what an edge gets
  deleted from. A tree carrying a checked-in `cargo-metadata.json` is read from
  it instead, which is how the fixtures work. The verdict is cached against the
  only inputs that can change it — `Cargo.lock` and every `Cargo.toml`, by size
  and mtime — so the gate pays for the walk once per manifest change, not once
  per commit. `roots = "*"` is every workspace package, which is how a rule
  like "no package may reach the scratch crate" is stated without re-listing
  the workspace forever; under the wildcard a package that depends on nothing
  is a leaf rather than a broken walk, so `min_reachable` is what answers
  vacuity there — a walk that reached fewer packages than the floor fails
  loudly instead of reporting clean.
- **`dep-graph-ceiling`** asks the complementary question of the same resolved
  graph, and shares its walk, its `roots = "*"` wildcard, its `min_reachable`
  floor and its cache. `counts` decides what a reached package is worth:
  the default counts only WORKSPACE members, `counts = "all"` counts every
  package including registry crates. A root reaching nothing countable is a
  hit of weight 0, not an offence — so its `clean/` fixture is judged against
  a ZERO baseline (every hit weighing 0 passes, any weight above it fails),
  which is what lets the kind have a clean fixture at all. Its `escape` is
  read from the ROOT's own `Cargo.toml`, as a `#` comment carrying a reason:
  a deliberate edge is admitted there, never by raising the baseline, which
  the staged-baseline guard refuses. An escaped root drops out of the ceiling
  for that run while its reachable packages still count toward the vacuity
  floor, so waiving one never turns a broken walk into a clean verdict.
- **`line-count`**'s `count = "code"` judges only lines that carry CODE: blank
  lines and comment-only lines (by extension — `//` and `/* */` for
  `.rs`/`.go`/`.ts`, `#` for `.py`/`.sh`/`.toml`) never count toward `max`. A
  baseline recorded under the default `count = "text"` still compares fine —
  a code count is always LOWER, so switching never looks like a regression —
  but the ceiling is now stale, and `check` says so once per site rather than
  silently tightening it away on the next run. `unit_split` (a regex) judges
  the file as TWO units against the same `max` when it matches a line — the
  matching line opens the second unit — keyed `<path>` and `<path>#tests`, so
  a test module inline with its source does not inflate the module's own debt
  and vice versa; a file the regex never matches is one unit, unchanged.
- **`doc-path-resolves`**'s resolution order is citing-file-relative first —
  a markdown link is written relative to the file that holds it — then the
  repo root, then the `crates/<x>`/`tools/<x>` locality convention — the first
  one that names a real file or directory wins.
- **`path-regex-absent`** judges the NAME, never the contents: a probe file
  called `task19_buckling.rs` is the offence, and reading it would never show
  that. Hits carry no line, so `expected.txt` in its fixtures lists bare paths.
- **`registry-both-ways`** reads uses out of whatever the scope includes, source
  or not: put `tools/**/*.sh` in `include` and a switch read only by a shell
  script counts as a use, so it is neither reported unregistered nor reported
  stale. `entry_column` (0-based) scopes `entry_pattern` to one cell of a
  `\|`-delimited registry line — a leading and trailing `\|` are stripped first,
  so column 0 is the first cell after that — which is what a markdown table
  needs: the crate column and the prose column beside it both carry backticked
  names, and separating them needs a lookbehind or a repeated capture group
  that Go's RE2 has neither of. A row with fewer cells than `entry_column`
  names (the table's own separator row, a stray `\|` in prose) registers
  nothing rather than reading the wrong column out of it. **This is the
  matcher that owns "every X is registered in Y"** — `file-set-containment`
  looks like the same shape but is for containment between two ARBITRARY
  files, one notation each; reach for `registry-both-ways` first for a
  registry, and only fall back to `file-set-containment` when the "registry"
  side does not have the two-pattern (entry vs. use) structure at all.
- **`file-set-containment`** is containment, never equality: the stand-in may
  refuse MORE than the real system, never less. A deliberate deviation puts the
  law's `escape` marker in `superset_file`, and a marker with nothing left to
  waive is itself a finding — stale waivers are how a guard quietly stops
  guarding. `capture` is a single regex applied to BOTH files, which only
  works when they spell the fact identically — the uncommon case. The normal
  case is two notations for one name (a Cargo.toml members line, `"crates/zone"`,
  against a markdown table cell, `` `zone` ``); `subset_capture` and
  `superset_capture`, given TOGETHER, are the two extraction patterns for that
  case. The two forms are exclusive: a law naming `capture` alongside either
  split field is rejected at load, never silently resolved by preferring one —
  preferring `capture` would leave the OTHER side's notation uncompared
  against anything, which defeats the law without saying so.
- **`json-number-ceiling`** is a MEASUREMENT law: every value it reads is a
  hit, weighted by the number (rounded up), and the `tolerance_pct` is applied
  when comparing to the baseline rather than when measuring — a figure inside
  tolerance still has to lower its ceiling. `enabled_env` arms it: unset, the
  law is skipped ENTIRELY (no check and no tighten — tightening against data
  that was never generated would wipe the baseline). A glob under `target/`
  reads from `CARGO_TARGET_DIR` when the environment sets one and
  `<root>/target` when it does not, while the KEY keeps its `target/` prefix
  either way — otherwise an environment variable would rewrite every baseline
  entry. Its `clean/` fixture is a file the glob must
  REFUSE (criterion's `base/` copy is the natural one), which is what proves
  the reader discriminates.

`key` is `file` (baseline `<file> | <count>`) or `file:line-content-hash`
(baseline one line per occurrence, written `<path> | <trimmed line>`).

For a line-keyed law the identity is the **trimmed offending line, and only
that**: the baseline is a MULTISET of offending text over the whole workspace,
and the path is written down for the reader rather than compared. So a `git mv`
or a crate rename is not a regression — the same lines are still there, in the
same number — while adding one more occurrence of a line already at its
ceiling IS one, wherever it lands, which a per-file count cannot see (it would
read the new file as a brand-new key and the old file as unchanged). Line
NUMBERS are not part of the identity either: inserting a line above an offence
changes nothing. Swapping one offending site for a DIFFERENT line still
regresses, because the new text is a new identity at a ceiling of zero.
Tightening rewrites each surviving row's path from a site the scan actually
found, so a row never dangles at a file that has moved, and drops the rows
whose text no longer appears that many times. A count-keyed (`file`) law
measures a property OF a file — its length — so there the path IS the
identity and a rename is a new key at a ceiling of zero.

The pre-edit hook judges ONE file, so it cannot see a workspace total: an
added line whose text is already at its ceiling somewhere else is caught by
the whole-tree run at commit, not by the write.

#### Landing a new matcher field

This repo's own commit gate runs the GLOBALLY INSTALLED `aphrollo` binary,
which predates any matcher kind or field this checkout's engine code just
added. A law under `.ratchet/laws/` naming that kind or field would reject
every commit here until the binary is rebuilt post-merge — so a new
capability lands in three separate steps, never one:

1. Land the engine change (`internal/ratchet`), proved by `RunFixtures()`
   against synthetic trees under `t.TempDir()`, never against this repo's
   own tracked `.ratchet/fixtures/`. Name the kind (or the kind and field)
   in `matcherUsageAllowlist` (`internal/ratchet/law_matcher_usage_test.go`)
   as `matcherUsageBootstrap`, with the `Ref` of the issue or PR that owes
   the real law, so nothing silently forgets the capability has no real
   user yet.
2. Merge, and let the box's installed `aphrollo` binary get rebuilt against
   the new commit.
3. Land the real law under `.ratchet/laws/` and its tracked fixtures under
   `.ratchet/fixtures/`, and remove the `matcherUsageAllowlist` entry in the
   same commit.

`TestMatcherUsage_EveryKindAndOptionalFieldHasARealLawOrAnAllowlistEntry`
enforces step 1 stays honest and step 3 actually happens: it enumerates the
matcher kinds and optional fields the engine's own `matcherKeys` map
accepts, cross-references every real law under this repo's `.ratchet/laws`,
and fails on either a kind/field with no law and no allow-list entry, or an
allow-list entry a law now exercises (stale — a fixed gap left in the list
would let this check nag forever about something already settled). Like a
baseline, the allow-list only ever gets shorter: widening it back out after
a law is removed on purpose is a decision the test forces onto the same
commit, with the reason stated, never a silent ratchet up.

The allow-list carries two categories, not one flat list with a free-text
reason, because "awaiting its bootstrap commit" and "this repo has no
occasion for it" are different populations that happen to share one
symptom — a kind or field no real law here exercises. Mixing them hides the
few that matter among the many that do not: within a month nobody re-reads
a wall of reasons, and a bootstrap entry that has sat for six months reads
exactly like one that will never move.

- **`matcherUsageBootstrap`** is DEBT with a named owner: `Ref` is the
  issue or PR that owes the real law (`ident-resolves` owes `#324`,
  `registry-both-ways`'s `entry_column` owes `#492`), required on every
  entry in this category — debt with nobody named as owing it is debt that
  gets lost, which is #501's own failure mode. It is meant to shrink to
  zero and stay there; the test logs its current members every run
  (`3 kind(s)/field(s) awaiting their bootstrap law: …`) so the count stays
  visible without dumping the much larger `unused-here` population beside
  it.
- **`matcherUsageUnusedHere`** is NOT debt: this repo's own dogfood law set
  has no occasion for the kind or field — several are cargo/JSON-shaped
  matchers a Go-only repo's laws never need, or a capability aimed at a
  downstream CONSUMING repo (`dep-graph-ceiling`'s own `#437`/`#480` were
  reported and fixed for borld, not this repo). `Ref` is refused on this
  category: nobody owes it a law, so there is nothing to name as owing one.
  It may sit here indefinitely — that is the correct steady state, not a
  backlog — and the check never mistakes it for one, because it is never
  logged as awaiting anything.

Neither category sees a LAW that was considered and declined on real
evidence rather than never attempted: `#324`'s own `readme_flag_registry`
measured 40 stale and 17 unregistered names against 69 defined flags and
was dropped as noise, but its kind, `registry-both-ways`, already has a
real law elsewhere, so this check never had an opinion on it either way. A
future decline that DOES leave a kind or field with no real law belongs in
`matcherUsageUnusedHere`, its reason naming the decision rather than
reading like a TODO.

#### Diff-scoped kinds

`symbol-removed` answers a different question from every other kind above:
not "does the tree, right now, obey the rule" but "did something the tree
used to carry silently vanish". It needs the OTHER side of a diff, so it
takes `--base <ref>` (git `pre-commit`/`pre-merge-commit` pass `HEAD`
automatically; `ratchet check` run by hand needs `--base` given) and, for
every file its `[scope]` matches, collects every name `pattern`'s one
capture group caught at `base` and at the current tree — the CURRENT tree,
never `base` twice, so `--proposed` and a staged `--files` narrowing are
honored on the tip side exactly as every other kind honors them. A name
present at `base` and absent everywhere at tip is a hit, keyed `<base
path>:<name>` — the base path, not wherever (if anywhere) the name turns up
again, so a plain rename reports under the OLD name while a name that moved
to a different file, unchanged, reports nothing at all. The one way through
besides restoring it is a tombstone comment left where the symbol stood:
`// ratchet: <law name> <symbol>: <reason>` (or `#`), with a REASON after
the colon — a stub with nothing after it admits nothing.

A tombstone may name a repo-relative PATH instead of a symbol (anything
carrying a `/` or a `.` is read as one), and then it admits every symbol that
stood in that file at base. That is for the case #574 measured — a whole test
file deleted along with the subject its tests exercised, which cost one lane
318 of its 369 tombstone lines, each naming a test whose subject was already
gone. It is the narrowest reading of the claim, because a bulk admission is
also what a cheat looks like: the file must be GONE at tip (a file still
standing admits nothing, so deleting the one failing test out of a surviving
file still needs a tombstone naming that test), and NOTHING captured in it at
base may survive anywhere at tip (a file whose tests turn up elsewhere was
split, not retired, and each still-missing test needs its own line). Neither
condition is a statement of intent; both are checked against the tip.

Run with no base at
all (the pre-edit hook's shape: one `--proposed` file and nothing else) the
law answers nothing rather than guessing, and says so once in a note rather
than reporting a false clean. It carries no baseline file of its own: every
hit is a regression from a ceiling of zero, paid down only by a tombstone or
a restore, never by a floor row creeping up.

#### The baseline law

A baseline is a **ceiling per key** and it only ever goes down:

- measured **above** it → a regression, reported and (for `deny`) rejected;
- measured **below** it → the run that saw the fix lowers or drops the entry
  and rewrites the file: atomically (tmp + rename), byte-stable when nothing
  moved, preserving header and mid-file comments in place and whatever line
  ending is already on disk. A line-keyed row is also re-pathed from the sites
  the scan found, so the same run that leaves the count alone still stops a
  row from naming a file that has moved;
- it **never raises** a count and **never adds** a key. The only way to admit
  a new hit is the law's own escape comment.

`ratchet check` tightens by default (`--no-tighten` to report only); a run
carrying `--proposed` never tightens, because the tree it measured does not
exist. The gate does not tighten either — a commit hook that rewrote a file
mid-commit would leave the lowered ceiling unstaged.

At commit and merge time the scan sees TRACKED files only: every path in the
index, judged as the index has it, which is exactly what the commit will
contain. An untracked or ignored file is part of no commit — another session's
scaffolding in a shared checkout, a scratch note, a generator's leftovers —
and a merge refused over one cannot be cleared by changing anything in the
merge. Staging the file is what makes it answer. Pre-edit denial is the other
side and judges the file being written whether git has seen it or not.

#### Baselines are never raised by hand

A baseline is a ceiling that only ever goes down, and it lives in a text file
any editor can widen — which happened: a `1048` entry was hand-edited to `1049`
to get a commit through. So the ban is mechanical. `precommit` and
`premergecommit` parse every STAGED baseline old-vs-new, in both the counted
and multiset forms, and reject a key whose count ROSE or which is NEW — counting a multiset row by
its TEXT, the identity the engine uses, so a legitimate re-path is not read as
a brand-new key:

```
gate precommit: baseline-rejected: crates/ratchet/tests/module_size_baseline.txt crates/a.rs 1048 -> 1049
  baselines are written by the ratchet itself; lower the code, or use the law's escape comment
```

Lowering, removing and header edits pass — that is what a fix looks like — and
a baseline file that is new in the commit passes too, since adopting a law is
not raising a ceiling. The guarded globs default to `.ratchet/baselines/*.txt`
and `crates/ratchet/tests/*_baseline.txt`; a workspace can declare its own with
`baselines = [...]` under `[workspace.metadata.aphrollo]`, which REPLACES the
defaults.

#### Fixtures — a law nobody proved catches nothing

`ratchet test` runs each law over its own `fixtures/<law>/hit` files, requires
exactly the offences listed in `expected.txt` (`<file>:<line>` per line, or the
hit's key for a whole-tree law, which has no line to point at), and
requires `clean/` to produce none. Both directions are required: hit-only
proves a rule fires, never that it discriminates. A law with no fixtures
fails. The commit gate runs `ratchet test` whenever a commit stages anything
under `.ratchet/`.

A fixture tree is laid out the way the REPO is, because the fixture root
stands in for the repo root and the law's own `include` globs decide what it
reads — an `include` of `crates/**/*.rs` reaches the first of these and never
the second:

```text
hit/crates/a/src/bare.rs
hit/bare.rs
```

A fixture the scope could never reach fails the test rather than being
skipped — otherwise a typo in `include` disarms the law in the real tree while
its fixtures stay green, which is the exact failure fixtures exist to catch.

A fixture is never compiled — the ratchet reads it as text, exactly like the
tree it stands in for — which is what the gate's own `<path> has no owning
cargo package — not tested` line is reporting: correct and harmless, but it
means an invalid fixture never fails as a syntax error. A typo in a string
literal or an unbalanced brace still scans, the law still counts whatever its
matcher sees, and the mistake surfaces as a wrong hit count rather than a
compiler error. That is tolerable for a matcher that only reads lines; it
sharpens for a `code_only` law, whose comment-stripping depends on the file
actually parsing — a fixture with an unterminated string or an unbalanced
block comment can make the law look right against input no compiler would ever
accept. Where a law reads STRUCTURE rather than lines, write the fixture so it
would compile, and keep it small enough to check by eye.

#### Pre-edit denial

The PreToolUse hook reconstructs what a `Write`/`Edit`/`MultiEdit` would leave
on disk (old/new strings applied to the file, MultiEdit in order) and judges
that content, narrowed to the one file:

```
ratchet: nan-guard: crates/pose/src/advance.rs:212 let a = x.clamp(0.0, 1.0); (baseline 0, now 1) — escape: // nan-safe: <why> on the line or the line above
```

Every hit is ONE line and every line ends with the way through, so the fix is
legible from the denial and nobody has to open the law: the escape comment to
write and where it may sit, `— no escape: lower the code` for a law that has
none, and `— split the file; the ceiling is N` for a count-keyed law, which is
paid down rather than waived. The line is bounded at 160 characters and it is
the OFFENDING TEXT that gets truncated — a remedy that scrolled off the end is
a remedy nobody read.

A `deny` law with a new hit exits 2 and the write never happens; a `warn` law
prints the line once and allows. Everything here fails **open** — a malformed
payload, an unreadable file or a broken law file must never wedge a session
over a rule that is itself broken. A narrowed run reports uses nobody
registered but never claims a registry line is stale: that needs the whole
tree.

#### Adopting a baseline (`--adopt`)

`check`'s tighten only ever LOWERS or REMOVES a row — never creates a file,
never raises one — which is correct for the everyday path but leaves no way
for a law to land its FIRST baseline, or for a deliberately widened one to
land its next: a brand-new deny law with real hits gets no baseline file at
all, and widening an existing law's scope just reports every newly-reached
hit as a regression forever. `ratchet check --adopt <law>` is the deliberate
door: it writes that law's baseline from what the tree measures RIGHT NOW,
raising or creating rows as needed, allowed only when the law has no baseline
file yet, or its `.toml` differs from HEAD — otherwise it refuses with one
line naming the law and the reason, so `--adopt` can never quietly launder an
unrelated raise. The commit-time staged-baseline guard (below) knows the same
rule: a raised or brand-new row is accepted when the law that owns the
baseline is ITSELF staged with a `[matcher]` or `[scope]` change in the same
commit, logged `baseline-adopted:<law>:<rows>`, and refused exactly as before
otherwise.

#### The law library (`ratchet init` / `ratchet presets`)

A known-good law does not have to be typed from scratch in every repo: this
binary embeds a set of PRESETS — ordinary law bodies, grouped `common` /
`rust` / `go`, with a `{{name}}` slot wherever a value is repo-specific (a
banned pattern, an env-prefix list, a dependency-graph root).
`aphrollo ratchet presets` lists every one, group/name and the params its
template asks for; `aphrollo ratchet init --preset common,rust --param
pattern=TODO\( --param prefixes=BORLD` copies each preset in those groups
into `.ratchet/laws/*.toml`, substituting `--param name=value` into its `{{name}}`
slots and adding `extends = "preset:<group>/<name>"` plus a `[params]` table
recording what it used. A preset with an unfilled slot is `[skip]`ped, named,
with what `--param` it still needs — nothing half-rendered is ever written —
and a name already under `laws/` is `[skip]`ped too: `init` is idempotent,
safe to re-run over a partly-adopted set.

The written file is otherwise an ORDINARY law: `extends` and `[params]`
change nothing about how it is scanned. What they buy is drift detection —
`ratchet check` re-renders the named preset with the law's own `[params]` and
warns, once per law, when its `[matcher]` no longer matches: a fork nobody
flagged as one. `doc_reference_exists` is also how `aphrollo docs check`
finds its rule when a repo declares no law of its own — see [Doc-reference
guard](#doc-reference-guard-aphrollo-docs-check).

#### Commands

```sh
aphrollo ratchet check                       # judge the tree, tighten, exit 1 on a deny regression
aphrollo ratchet check --only nan-guard      # one law
aphrollo ratchet check --format json         # what the hooks read
aphrollo ratchet check --no-tighten          # report only
aphrollo ratchet check --proposed crates/a.rs=/tmp/new.rs   # judge content not on disk
aphrollo ratchet check --adopt nan-guard     # write nan-guard's baseline from the tree (new or widened law only)
aphrollo ratchet check --base HEAD~1         # judge a diff-scoped law (symbol-removed) against that ref
aphrollo ratchet test                        # prove every law against its fixtures
aphrollo ratchet presets                     # list every embedded preset and its params
aphrollo ratchet init --preset common,rust --param pattern=TODO\( --param prefixes=BORLD
```

A repeat `check` costs milliseconds: every file's hits are cached under the
state dir, keyed by path + size + mtime **and** a hash of the law set, so a
rule that changed drops the cache instead of inheriting verdicts reached under
the old one. A repo with no laws dir under `.ratchet` says `no laws` and exits 0.
