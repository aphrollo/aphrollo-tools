package report

// pageTemplate is the one page: inline CSS, no script, nothing fetched. It
// reads the report model; html/template escapes every value it prints.
const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.R.Title}}: {{.R.Repo}}</title>
<style>
:root { color-scheme: light dark;
  --bg:#f6f5f1; --fg:#1b1d20; --muted:#5f646b; --line:#d9d7d0; --soft:#ecebe5; --code:#e9e7df;
  --ink:#24578a; --s1:#24578a; --s2:#a8641f; --s3:#8a8f96; --up:#a3311f; --down:#2b7344; --sel:#c9dcf0; }
@media (prefers-color-scheme: dark) { :root {
  --bg:#121417; --fg:#e4e6e8; --muted:#a0a6ad; --line:#2d3137; --soft:#1b1e22; --code:#23272d;
  --ink:#7fb0e6; --s1:#7fb0e6; --s2:#e0a35c; --s3:#80868e; --up:#ff9a8a; --down:#7fd39a; --sel:#2c4766; } }
* { box-sizing: border-box; }
html { scrollbar-color: var(--line) var(--bg); }
body { margin:0; background:var(--bg); color:var(--fg); font:16px/1.55 system-ui, -apple-system, "Segoe UI", sans-serif; font-variant-numeric: tabular-nums; }
::selection { background:var(--sel); }
a { color:var(--ink); text-underline-offset:0.2em; }
:focus-visible { outline:2px solid var(--ink); outline-offset:2px; border-radius:2px; }
main { max-width: 960px; margin: 0 auto; padding: 32px 24px 64px; }
header { margin-bottom: 8px; }
h1 { font-size: 1.75rem; line-height:1.2; letter-spacing:-0.02em; margin: 0 0 4px; }
h2 { font-size: 1.3rem; line-height:1.25; letter-spacing:-0.01em; margin: 0 0 12px; }
h3 { font-size: 1rem; margin: 24px 0 8px; }
p { margin: 0 0 12px; max-width: 72ch; }
.sub, .muted, .note, .none { color:var(--muted); }
.note { font-size: 0.9rem; }
.lede { font-size: 1.15rem; margin: 16px 0; }
.facts { display:grid; grid-template-columns: repeat(auto-fill, minmax(10.5rem, 1fr)); gap: 12px 24px; margin: 0 0 8px; padding: 16px 0; border-top:1px solid var(--line); border-bottom:1px solid var(--line); }
.facts dt { color:var(--muted); font-size:0.85rem; }
.facts dd { margin:0; font-size:1.2rem; font-weight:600; }
.chg { font-size:0.85rem; font-weight:500; }
.up { color:var(--up); } .down { color:var(--down); }
nav { display:flex; flex-wrap:wrap; gap: 4px 16px; margin: 12px 0 0; font-size:0.9rem; }
section { padding: 32px 0 8px; border-top: 1px solid var(--line); margin-top: 24px; }
.scroll { overflow-x:auto; margin: 0 0 12px; }
table { border-collapse: collapse; width:100%; font-size: 0.9rem; }
th, td { text-align:left; padding:6px 12px 6px 0; border-bottom:1px solid var(--line); vertical-align: top; }
th { color:var(--muted); font-weight:600; font-size:0.8rem; }
td:first-child { overflow-wrap:anywhere; min-width: 9rem; }
td.n, th.n { text-align:right; white-space:nowrap; }
code { background:var(--code); border-radius:4px; padding:1px 5px; font:0.82rem ui-monospace, "Cascadia Mono", Menlo, monospace; user-select: all; white-space:nowrap; }
.refs { display:block; color:var(--muted); font-size:0.82rem; margin-top:2px; }
details { margin: 4px 0; }
summary { cursor:pointer; color:var(--ink); width:max-content; max-width:100%; }
.refs details { font-family: ui-monospace, "Cascadia Mono", Menlo, monospace; overflow-wrap:anywhere; }
.over { color:var(--up); font-weight:600; }
.group { margin: 0 0 20px; }
.group h3 { margin: 0 0 6px; }
.group ul { list-style:none; margin:0; padding:0; }
.group li { padding: 6px 0; border-bottom:1px solid var(--line); }
.group li:last-child { border-bottom:0; }
.rule { font-weight:600; overflow-wrap:anywhere; }
figure { margin: 0 0 20px; }
figcaption { font-size:0.85rem; color:var(--muted); margin-bottom:8px; }
.bars .row { display:grid; grid-template-columns: minmax(7rem, 16rem) 1fr minmax(4rem, auto); gap: 4px 12px; align-items:center; padding: 3px 0; font-size:0.85rem; }
.bars .lbl { text-align:right; overflow-wrap:anywhere; line-height:1.3; }
.bars .val { white-space:nowrap; }
.track { display:block; height:12px; background:var(--soft); border-radius:3px; }
.bar { display:block; height:100%; min-width:1px; background:var(--ink); border-radius:3px; }
.stack { display:flex; height:16px; border-radius:3px; overflow:hidden; background:var(--soft); }
.seg { display:block; height:100%; }
.s1 { background:var(--s1); } .s2 { background:var(--s2); } .s3 { background:var(--s3); }
.legend { list-style:none; display:flex; flex-wrap:wrap; gap: 4px 20px; padding:0; margin: 8px 0 0; font-size:0.85rem; }
.key { display:inline-block; width:10px; height:10px; border-radius:2px; margin-right:6px; }
.trend svg { display:block; width:100%; height:96px; }
.trend .line { fill:none; stroke-width:2; vector-effect:non-scaling-stroke; }
.trend .area { stroke:none; opacity:0.12; }
.trend .l1 { stroke:var(--s1); } .trend .area.l1 { fill:var(--s1); }
.trend .l2 { stroke:var(--s2); stroke-dasharray:6 3; } .trend .area.l2 { fill:var(--s2); }
.trend .axis { stroke:var(--line); vector-effect:non-scaling-stroke; }
.xs { display:flex; justify-content:space-between; font-size:0.8rem; color:var(--muted); }
@media (max-width: 560px) {
  main { padding: 20px 16px 48px; }
  h1 { font-size: 1.45rem; }
  .bars .row { grid-template-columns: 1fr auto; }
  .bars .lbl { grid-column: 1 / -1; text-align:left; }
  td:first-child { min-width: 7rem; }
}
</style>
</head>
<body>
<main>
<header>
<h1>{{.R.Title}}</h1>
<p class="sub">{{.R.Repo}} · {{.R.Window}} up to {{.R.Until}} · {{num .R.Events}} events. Replay an event by running its <code>aphrollo why &lt;seq&gt;</code> text in the repo.</p>
<p class="lede">{{.Lede}}</p>
<section id="changed">
<h2>Changed</h2>
<ul>
{{range .Changed}}<li>{{.}}</li>
{{end}}</ul>
</section>
<dl class="facts">
{{range .Facts}}<div><dt>{{.Name}}</dt><dd>{{.Value}}{{if .Change}} <span class="chg {{if .Worse}}up{{else}}down{{end}}">({{.Change}})</span>{{end}}</dd></div>
{{end}}</dl>
{{with .R.Previous}}<p class="note">In brackets: the change against the {{.Window}} before, which held {{num .Events}} events.</p>{{end}}
<nav aria-label="sections"><a href="#changed">Changed</a><a href="#speed">Speed</a><a href="#proposals">Proposals</a><a href="#friction">Friction</a><a href="#wrong">Wrong blocks</a><a href="#escapes">Escapes</a><a href="#ab">A/B and shadow</a><a href="#tokens">Injected tokens</a>{{if .R.Usage}}<a href="#usage">Session usage</a>{{end}}</nav>
</header>

<section id="speed">
<h2>Speed</h2>
<p class="note">Seconds per run, green or not, over the window. Change is the p50 against the window before; a slower p50 is worse, and ~ is no clear change (it shows only with four runs on each side and a Mann-Whitney p under 0.05). Indented rows are the binary versions of the window; a version is confounded with the work done that week. Runs of no binary version are left out of the version split (they stay in the row above), and a first version with fewer than four runs stays on its own.</p>
{{if .R.Speed.Rows}}<div class="scroll"><table><tr><th>Stage</th><th class="n">Runs</th><th class="n">p50</th><th class="n">p90</th><th class="n">Max</th><th class="n">Change</th></tr>
{{range .R.Speed.Rows}}<tr><td>{{.Stage}}</td><td class="n">{{num .N}}</td><td class="n">{{secs .P50}}</td><td class="n">{{secs .P90}}</td><td class="n">{{secs .Max}}</td>{{speedDelta .}}</tr>
{{range .Versions}}<tr class="ver"><td>&nbsp;&nbsp;{{.Label}}</td><td class="n">{{num .N}}</td><td class="n">{{secs .P50}}</td><td class="n">{{secs .P90}}</td><td class="n">{{secs .Max}}</td>{{versionDelta .}}</tr>
{{end}}{{end}}</table></div>{{else}}<p class="none">no runs timed in the window</p>{{end}}
{{range .R.Speed.Gaps}}<p class="note">not derivable: {{.}}</p>
{{end}}</section>

<section id="proposals">
<h2>Proposals</h2>
<p class="note">The report proposes; nothing here is applied. Rules are grouped by the change they call for.</p>
{{range .Groups}}<div class="group"><h3>{{.Change}}</h3><ul>
{{range .Proposals}}<li><span class="rule">{{.Rule}}</span> <span class="muted">· {{.Numbers}}</span>{{refs .Refs}}</li>
{{end}}</ul></div>
{{else}}<p class="none">none: no rule is over a threshold</p>{{end}}
</section>

<section id="friction">
<h2>Friction per rule</h2>
{{.Charts.Friction}}
{{if .Friction}}<div class="scroll"><table>{{template "fhead" $}}
{{range .Friction}}{{template "frow" (frictionRow $ .)}}{{end}}</table></div>
{{if .FrictionMore}}<details><summary>{{len .FrictionMore}} more rules</summary><div class="scroll"><table>{{template "fhead" $}}
{{range .FrictionMore}}{{template "frow" (frictionRow $ .)}}{{end}}</table></div></details>{{end}}
{{else}}<p class="none">none</p>{{end}}
{{with .R.Previous}}{{if .Gone}}<p class="note">Friction the window before and none in this one: {{range $i, $g := .Gone}}{{if $i}}, {{end}}{{$g.Rule}} ({{$g.N}}){{end}}.</p>{{end}}{{end}}
<p class="note">Waited is gate time the agent stood waiting on; background is gate time that ran while it kept working.</p>
</section>

<section id="wrong">
<h2>Wrong-block candidates</h2>
{{.Charts.Wrong}}
{{if .Waived}}<div class="scroll"><table><tr><th>Rule</th><th class="n">Denies</th><th class="n">Waived</th><th class="n">Rate</th></tr>
{{range .Waived}}<tr><td>{{.Rule}}{{refs .Refs}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Waived}}</td><td class="n">{{.Rate}}</td></tr>
{{end}}</table></div>{{else}}<p class="none">no deny was waived by an override</p>{{end}}
{{if .R.ShadowWrong}}<h3>Shadow would-be blocks</h3><p class="note">Blocks the kernel would have made where aphrollo did not, and how the log judged them afterwards.</p><div class="scroll"><table><tr><th>Rule</th><th class="n">Fires</th><th class="n">Would-be blocks</th><th class="n">Wrong</th><th class="n">Caught</th><th class="n">Open</th></tr>
{{range .R.ShadowWrong}}<tr><td>{{.Rule}}</td><td class="n">{{.Fires}}</td><td class="n">{{.Stricter}}</td><td class="n">{{.Wrong}}</td><td class="n">{{.Catches}}</td><td class="n">{{.Open}}</td></tr>
{{end}}</table></div>{{end}}
{{if .R.Standdowns}}<details><summary>{{len .R.Standdowns}} matchers made a rule stand down</summary><p class="note">A stand-down is a rule that let the edit through because the kernel cannot run its matcher yet.</p><div class="scroll"><table><tr><th>Matcher</th><th class="n">Times</th></tr>
{{range .R.Standdowns}}<tr><td>{{.Matcher}}{{refs .Refs}}</td><td class="n">{{.N}}</td></tr>
{{end}}</table></div></details>{{end}}
</section>

<section id="escapes">
<h2>Escapes and the stage that should have caught them</h2>
{{.Charts.Escapes}}
{{if .R.Escapes.Rows}}<div class="scroll"><table><tr><th>Class</th><th class="n">N</th><th>Should have been caught by</th></tr>
{{range .R.Escapes.Rows}}<tr><td>{{.Class}}{{refs .Refs}}</td><td class="n">{{.N}}</td><td>{{.Caught}}</td></tr>
{{end}}</table></div>{{else}}<p class="none">none</p>{{end}}
<p class="note">False positives (wrong denies): {{.R.Escapes.FalsePositives}}</p>
</section>

<section id="ab">
<h2>A/B and shadow, per arm and language</h2>
{{.Charts.AB}}
<p class="note">{{if .R.ABTotal.Decidable}}Decided on escapes per lane: {{.R.ABTotal.Verdict}}.{{else}}Still deciding on escapes per lane; an arm stops at 50 lanes in the whole log.{{end}}</p>
{{if .ABInWindow}}<h3>This window</h3><div class="scroll"><table><tr><th>Arm</th><th class="n">Lanes</th><th class="n">Denies</th><th class="n">Warnings</th><th class="n">Overrides</th><th class="n">Escapes</th><th class="n">Held out</th><th class="n">Budget drops</th></tr>
{{range .R.AB.Arms}}<tr><td>{{.Arm}}</td><td class="n">{{.Lanes}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Warnings}}</td><td class="n">{{.Overrides}}</td><td class="n">{{.Escapes}}</td><td class="n">{{.HeldOut}}</td><td class="n">{{.Dropped}}</td></tr>
{{end}}</table></div>
{{if .R.AB.Languages}}<div class="scroll"><table><tr><th>Arm</th><th>Language</th><th class="n">Lanes</th><th class="n">Denies</th><th class="n">Warnings</th></tr>
{{range .R.AB.Languages}}<tr><td>{{.Arm}}</td><td>{{.Lang}}</td><td class="n">{{.Lanes}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Warnings}}</td></tr>
{{end}}</table></div>{{end}}{{else}}<p class="none">no A/B lane in this window</p>{{end}}
<h3>Shadow</h3>
<p class="note">{{num .R.Shadow.Fires}} fires, {{num .R.Shadow.Dropped}} dropped for the budget. Held out: lanes kept out of the comparison; budget drops: checks skipped to stay inside the hook's time budget.</p>
{{if .R.Shadow.Languages}}<div class="scroll"><table><tr><th>Language</th><th class="n">Fires</th><th class="n">Agree</th><th class="n">Held out</th><th class="n">Budget drops</th></tr>
{{range .R.Shadow.Languages}}<tr><td>{{.Lang}}</td><td class="n">{{.Fires}}</td><td class="n">{{if .Rate}}{{.Rate}}{{else}}under 10 fires{{end}}</td><td class="n">{{.HeldOut}}</td><td class="n">{{.Dropped}}</td></tr>
{{end}}</table></div>{{end}}
</section>

<section id="tokens">
<h2>Token cost of what the harness injects</h2>
{{.Charts.Briefs}}
{{if .R.Tokens.Briefs}}<div class="scroll"><table><tr><th>Text</th><th class="n">Tokens</th><th class="n">Cap</th></tr>
{{range .R.Tokens.Briefs}}<tr><td>{{.Name}}</td><td class="n{{if .Over}} over{{end}}">{{num .Tokens}}</td><td class="n">{{num .Cap}}</td></tr>
{{end}}</table></div>{{else}}<p class="none">no briefs measured</p>{{end}}
{{if .R.Tokens.Biggest}}<h3>Biggest gate lines</h3><div class="scroll"><table><tr><th>Line</th><th class="n">Times</th><th class="n">Tokens</th></tr>
{{range .R.Tokens.Biggest}}<tr><td>{{.Name}}{{refs .Refs}}</td><td class="n">{{num .N}}</td><td class="n">{{num .Tokens}}</td></tr>
{{end}}</table></div>{{end}}
</section>

{{with .R.Usage}}<section id="usage">
<h2>Session usage</h2>
<p class="note">From the harness's local transcripts of {{.Repo}}: {{num .Sessions}} sessions, {{num .Total.Turns}} turns (a reply counted once), unreadable {{.Unreadable}} lines and {{.UnreadableFiles}} files. Aggregates only: no prompt, code, tool text or injected text is on this page. Cost is a notional list price computed when the report is read.</p>
<p>Total: input {{tok .Total.Submitted}} (fresh {{tok .Total.Fresh}}, cache write {{tok .Total.CacheWrite}}, cache read {{tok .Total.CacheRead}}), output {{tok .Total.Output}}, {{usd .Total.CostUSD}}.</p>
{{$.Charts.Days}}
{{$.Charts.Split}}
<p class="note">A record's working directory names the primary checkout, so a lane is joined from the event log and the worktree calls. Coordination: coordinator turns with no lane in reach. Unattributed: subagent turns with no lane event.</p>
{{$.Charts.Lanes}}
<div class="scroll"><table>{{template "uhead" "Lane"}}
{{range $.Lanes}}{{template "urow" .}}{{end}}</table></div>
{{if $.LanesMore}}<details><summary>{{len $.LanesMore}} more lanes</summary><div class="scroll"><table>{{template "uhead" "Lane"}}
{{range $.LanesMore}}{{template "urow" .}}{{end}}</table></div></details>{{end}}
{{$.Charts.Models}}
<div class="scroll"><table>{{template "uhead" "Role"}}
{{range .ByRole}}{{template "urow" .}}{{end}}</table></div>
<h3>Top sessions</h3>
<div class="scroll"><table>{{template "uhead" "Session"}}
{{range .TopSessions}}<tr><td title="{{.Key}}">{{short .Key}}</td><td class="n">{{num .Turns}}</td><td class="n">{{tok .Output}}</td><td class="n">{{usd .CostUSD}}</td></tr>
{{end}}</table></div>
<h3>aphrollo's injected text</h3>
<p>aphrollo injected about {{tok .Injection.Tokens}} tokens (first injection; cache re-reads are not counted){{if .Injection.FreshShare}}, {{.Injection.FreshShare}} of the fresh (non-cache-read) input{{end}}.</p>
{{$.Charts.Injection}}
{{with .Compare}}<h3>Before and after {{.At}}</h3>
{{$.Charts.Compare}}
<div class="scroll"><table><tr><th></th><th class="n">Days</th><th class="n">Cost a day</th><th class="n">Output a day</th><th class="n">Injected share</th></tr>
{{range $.ComparePeriods}}<tr><td>{{.Name}}</td><td class="n">{{.Days}}</td><td class="n">{{usd .CostPerDay}}</td><td class="n">{{tok .OutputPerDay}}</td><td class="n">{{.Share}}</td></tr>
{{end}}</table></div>{{end}}
</section>{{else}}<section id="usage"><h2>Session usage</h2><p class="none">no transcripts were read</p></section>{{end}}
</main>
</body>
</html>
{{define "fhead"}}<tr><th>Rule</th><th class="n">Denies</th><th class="n">Overrides</th><th class="n">Refusals</th><th class="n">Not tested</th><th class="n">Waited</th><th class="n">Background</th>{{if .R.Previous}}<th class="n">Change</th>{{end}}</tr>{{end}}
{{define "frow"}}{{with .F}}<tr><td>{{.Rule}}{{refs .Refs}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Overrides}}</td><td class="n">{{.Refusals}}</td><td class="n">{{.NotTested}}</td><td class="n">{{dur .SecsLost}}</td><td class="n">{{dur .SecsBackground}}</td>{{end}}{{if .Prev}}{{delta .Total .F.Prev}}{{end}}</tr>
{{end}}
{{define "uhead"}}<tr><th>{{.}}</th><th class="n">Turns</th><th class="n">Output</th><th class="n">Cost</th></tr>{{end}}
{{define "urow"}}<tr><td>{{.Key}}</td><td class="n">{{num .Turns}}</td><td class="n">{{tok .Output}}</td><td class="n">{{usd .CostUSD}}</td></tr>
{{end}}`
