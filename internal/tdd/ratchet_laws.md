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
| `line-count` | `max` | a file may not exceed `max` lines; key = file, count = lines | module-size debt |
| `regex-absent` | `pattern`, `key`, `count` | a pattern must NOT appear; `count = "matches"` counts every call on a line, not the line | the bare `.clamp(` guard |
| `path-regex-absent` | `pattern` | the repo-relative PATH must not match; key = the path, no line | a filename carrying a plan-item stamp or a serial letter |
| `regex-present` | `pattern` | every file in scope MUST contain it | a proptest that must carry an explicit seed |
| `marker-within-lines` | `trigger`, `marker`, `lines`, `contiguous`, `direction` | a `trigger` line requires a `marker` within N lines above (or below, or either), or in the comment run beside it | `// bound:` over a collection that grows |
| `registry-both-ways` | `registry_file`, `entry_pattern`, `use_pattern` | every use is registered AND every registry line is used; the LAST non-empty capture of a use match is the name, so an alternation with one group per branch works | the dev-instrument (env switch) registry |
| `doc-path-resolves` | `pattern` | a captured `.md` path must resolve at the repo root or inside the citing file's own `crates/<x>`/`tools/<x>` unit | doc citations |
| `dep-graph-forbids` | `roots`, `forbidden`, `edges`, `min_reachable` | no root package may REACH a forbidden one (glob) through the resolved dependency graph; `edges = "normal"` (default) never follows dev/build edges, which is the whole distinction | dev-only tooling in a shipping binary |
| `file-set-containment` | `superset_file`, `subset_file`, `capture` | every capture in `subset_file` must also appear in `superset_file` | a headless stand-in whose query must refuse at least what the real one refuses |
| `json-number-ceiling` | `files`, `path`, `tolerance_pct`, `enabled_env` | a number read out of generated JSON may not exceed its baseline by more than the tolerance | a criterion bench figure nobody was reading |

The last three judge a whole TREE rather than a file at a time, and each
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
- **`path-regex-absent`** judges the NAME, never the contents: a probe file
  called `task19_buckling.rs` is the offence, and reading it would never show
  that. Hits carry no line, so `expected.txt` in its fixtures lists bare paths.
- **`registry-both-ways`** reads uses out of whatever the scope includes, source
  or not: put `tools/**/*.sh` in `include` and a switch read only by a shell
  script counts as a use, so it is neither reported unregistered nor reported
  stale.
- **`file-set-containment`** is containment, never equality: the stand-in may
  refuse MORE than the real system, never less. A deliberate deviation puts the
  law's `escape` marker in `superset_file`, and a marker with nothing left to
  waive is itself a finding — stale waivers are how a guard quietly stops
  guarding.
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

#### Pre-edit denial

The PreToolUse hook reconstructs what a `Write`/`Edit`/`MultiEdit` would leave
on disk (old/new strings applied to the file, MultiEdit in order) and judges
that content, narrowed to the one file:

```
ratchet: nan-guard: crates/pose/src/advance.rs:212 let a = x.clamp(0.0, 1.0);
  (baseline 0, now 1; escape: // nan-safe:)
```

A `deny` law with a new hit exits 2 and the write never happens; a `warn` law
prints the line once and allows. Everything here fails **open** — a malformed
payload, an unreadable file or a broken law file must never wedge a session
over a rule that is itself broken. A narrowed run reports uses nobody
registered but never claims a registry line is stale: that needs the whole
tree.

#### Commands

```sh
aphrollo ratchet check                       # judge the tree, tighten, exit 1 on a deny regression
aphrollo ratchet check --only nan-guard      # one law
aphrollo ratchet check --format json         # what the hooks read
aphrollo ratchet check --no-tighten          # report only
aphrollo ratchet check --proposed crates/a.rs=/tmp/new.rs   # judge content not on disk
aphrollo ratchet test                        # prove every law against its fixtures
```

A repeat `check` costs milliseconds: every file's hits are cached under the
state dir, keyed by path + size + mtime **and** a hash of the law set, so a
rule that changed drops the cache instead of inheriting verdicts reached under
the old one. A repo with no laws dir under `.ratchet` says `no laws` and exits 0.
