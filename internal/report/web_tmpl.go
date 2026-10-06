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
:root { color-scheme: light dark; --bg:#fbfbfa; --fg:#1d1f21; --muted:#6b7075; --card:#ffffff; --line:#dcdedf; --bar:#3b6fb6; --bar2:#c0762b; --warn:#b3261e; --code:#eef0f2; }
@media (prefers-color-scheme: dark) { :root { --bg:#14161a; --fg:#e6e8ea; --muted:#9aa0a6; --card:#1c1f24; --line:#30343a; --bar:#6aa0e8; --bar2:#e0a35c; --warn:#ff8a80; --code:#262a30; } }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font:15px/1.5 system-ui, sans-serif; }
main { max-width: 860px; margin: 0 auto; padding: 16px; }
h1 { font-size: 1.4rem; margin: 8px 0 2px; } h2 { font-size: 1.1rem; margin: 0 0 8px; }
.sub { color:var(--muted); margin:0 0 16px; }
section { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:12px 14px; margin:14px 0; overflow-x:auto; }
table { border-collapse: collapse; width:100%; font-size: 0.92rem; }
th, td { text-align:left; padding:4px 8px 4px 0; border-bottom:1px solid var(--line); vertical-align: top; }
th { color:var(--muted); font-weight:600; }
td.n, th.n { text-align:right; font-variant-numeric: tabular-nums; }
code { background:var(--code); border-radius:4px; padding:1px 5px; font:0.85rem ui-monospace, monospace; user-select: all; }
.refs { color:var(--muted); font-size:0.85rem; }
.none, .note { color:var(--muted); }
.over { color:var(--warn); font-weight:600; }
.chart { width:100%; height:auto; display:block; margin:8px 0; }
.chart .lbl, .chart .val { font:11px system-ui, sans-serif; fill:var(--fg); }
.chart .bar { fill:var(--bar); }
.chart .axis { stroke:var(--line); }
.chart .line { stroke-width:2; } .chart .l1 { stroke:var(--bar); } .chart .l2 { stroke:var(--bar2); }
.chart .key.l1 { fill:var(--bar); } .chart .key.l2 { fill:var(--bar2); }
@media (max-width: 520px) { main { padding: 8px; } section { padding: 10px; } }
</style>
</head>
<body>
<main>
<h1>{{.R.Title}}</h1>
<p class="sub">{{.R.Repo}} · {{.R.Window}} up to {{.R.Until}} · {{.R.Events}} events. Replay an event by running its <code>aphrollo why &lt;seq&gt;</code> text in the repo.</p>

<section id="proposals">
<h2>Proposals</h2>
<p class="note">The report proposes; nothing here is applied.</p>
{{if .R.Proposals}}<table><tr><th>Rule</th><th>Evidence</th><th>Proposed change</th></tr>
{{range .R.Proposals}}<tr><td>{{.Rule}}</td><td>{{.Numbers}}<br>{{refs .Refs}}</td><td>{{.Change}}</td></tr>
{{end}}</table>{{else}}<p class="none">none: no rule is over a threshold</p>{{end}}
</section>

<section id="friction">
<h2>1. Friction per rule</h2>
{{.Charts.Friction}}
{{if .Friction}}<table><tr><th>Rule</th><th class="n">Denies</th><th class="n">Overrides</th><th class="n">Refusals</th><th class="n">Not tested</th><th class="n">Waited</th><th class="n">Background</th><th>Evidence</th></tr>
{{range .Friction}}<tr><td>{{.Rule}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Overrides}}</td><td class="n">{{.Refusals}}</td><td class="n">{{.NotTested}}</td><td class="n">{{dur .SecsLost}}</td><td class="n">{{dur .SecsBackground}}</td><td>{{refs .Refs}}</td></tr>
{{end}}</table>{{if .FrictionMore}}<p class="note">{{.FrictionMore}} more rows in the JSON.</p>{{end}}{{else}}<p class="none">none</p>{{end}}
<p class="note">Waited is gate time the agent stood waiting on; background is gate time that ran while it kept working.</p>
</section>

<section id="wrong">
<h2>2. Wrong-block candidates</h2>
{{.Charts.Wrong}}
{{if .Waived}}<table><tr><th>Rule</th><th class="n">Denies</th><th class="n">Waived</th><th class="n">Rate</th><th>Evidence</th></tr>
{{range .Waived}}<tr><td>{{.Rule}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Waived}}</td><td class="n">{{.Rate}}</td><td>{{refs .Refs}}</td></tr>
{{end}}</table>{{else}}<p class="none">no deny was waived by an override</p>{{end}}
{{if .R.ShadowWrong}}<h3>Shadow would-be blocks</h3><table><tr><th>Rule</th><th class="n">Fires</th><th class="n">Would-be blocks</th><th class="n">Wrong</th><th class="n">Caught</th><th class="n">Open</th></tr>
{{range .R.ShadowWrong}}<tr><td>{{.Rule}}</td><td class="n">{{.Fires}}</td><td class="n">{{.Stricter}}</td><td class="n">{{.Wrong}}</td><td class="n">{{.Catches}}</td><td class="n">{{.Open}}</td></tr>
{{end}}</table>{{end}}
{{if .R.Standdowns}}<h3>Stand-downs</h3><table><tr><th>Matcher</th><th class="n">Times</th><th>Evidence</th></tr>
{{range .R.Standdowns}}<tr><td>{{.Matcher}}</td><td class="n">{{.N}}</td><td>{{refs .Refs}}</td></tr>
{{end}}</table>{{end}}
</section>

<section id="escapes">
<h2>3. Escapes by class and the stage that should have caught them</h2>
{{.Charts.Escapes}}
{{if .R.Escapes.Rows}}<table><tr><th>Class</th><th class="n">N</th><th>Should have been caught by</th><th>Evidence</th></tr>
{{range .R.Escapes.Rows}}<tr><td>{{.Class}}</td><td class="n">{{.N}}</td><td>{{.Caught}}</td><td>{{refs .Refs}}</td></tr>
{{end}}</table>{{else}}<p class="none">none</p>{{end}}
<p class="note">False positives (wrong denies): {{.R.Escapes.FalsePositives}}</p>
</section>

<section id="ab">
<h2>4. A/B and shadow, per arm and language</h2>
{{.Charts.AB}}
<table><tr><th>Arm</th><th class="n">Lanes</th><th class="n">Denies</th><th class="n">Warnings</th><th class="n">Overrides</th><th class="n">Escapes</th><th class="n">Held out</th><th class="n">Budget drops</th></tr>
{{range .R.AB.Arms}}<tr><td>{{.Arm}}</td><td class="n">{{.Lanes}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Warnings}}</td><td class="n">{{.Overrides}}</td><td class="n">{{.Escapes}}</td><td class="n">{{.HeldOut}}</td><td class="n">{{.Dropped}}</td></tr>
{{end}}</table>
<p class="note">Lanes in the whole log: {{range .R.ABTotal.Arms}}{{.Arm}} {{.Lanes}} of 30 · {{end}}{{if .R.ABTotal.Decidable}}both arms have the lanes: the A/B can decide{{else}}not decidable yet{{end}}</p>
{{if .R.AB.Languages}}<table><tr><th>Arm</th><th>Language</th><th class="n">Lanes</th><th class="n">Denies</th><th class="n">Warnings</th></tr>
{{range .R.AB.Languages}}<tr><td>{{.Arm}}</td><td>{{.Lang}}</td><td class="n">{{.Lanes}}</td><td class="n">{{.Denies}}</td><td class="n">{{.Warnings}}</td></tr>
{{end}}</table>{{end}}
<h3>Shadow</h3>
<p class="note">{{.R.Shadow.Fires}} fires, {{.R.Shadow.Dropped}} dropped for the budget.</p>
{{if .R.Shadow.Languages}}<table><tr><th>Language</th><th class="n">Fires</th><th class="n">Agree</th><th class="n">Held out</th><th class="n">Budget drops</th></tr>
{{range .R.Shadow.Languages}}<tr><td>{{.Lang}}</td><td class="n">{{.Fires}}</td><td class="n">{{if .Rate}}{{.Rate}}{{else}}under 10 fires{{end}}</td><td class="n">{{.HeldOut}}</td><td class="n">{{.Dropped}}</td></tr>
{{end}}</table>{{end}}
</section>

<section id="tokens">
<h2>5. Token cost of what the harness injects</h2>
{{.Charts.Briefs}}
{{if .R.Tokens.Briefs}}<table><tr><th>Text</th><th class="n">Tokens</th><th class="n">Cap</th></tr>
{{range .R.Tokens.Briefs}}<tr><td>{{.Name}}</td><td class="n{{if .Over}} over{{end}}">{{.Tokens}}</td><td class="n">{{.Cap}}</td></tr>
{{end}}</table>{{else}}<p class="none">no briefs measured</p>{{end}}
{{if .R.Tokens.Biggest}}<h3>Biggest gate lines</h3><table><tr><th>Line</th><th class="n">Times</th><th class="n">Tokens</th><th>Evidence</th></tr>
{{range .R.Tokens.Biggest}}<tr><td>{{.Name}}</td><td class="n">{{.N}}</td><td class="n">{{.Tokens}}</td><td>{{refs .Refs}}</td></tr>
{{end}}</table>{{end}}
</section>

<section id="usage">
<h2>7. Session usage</h2>
{{with .R.Usage}}
<p class="note">From the harness's local transcripts of {{.Repo}}: {{.Sessions}} sessions, {{.Total.Turns}} turns (a reply counted once), unreadable {{.Unreadable}} lines and {{.UnreadableFiles}} files. Aggregates only: no prompt, code, tool text or injected text is on this page. Cost is a notional list price computed when the report is read.</p>
<p>Total: input {{tok .Total.Submitted}} (fresh {{tok .Total.Fresh}}, cache write {{tok .Total.CacheWrite}}, cache read {{tok .Total.CacheRead}}), output {{tok .Total.Output}}, {{usd .Total.CostUSD}}.</p>
{{$.Charts.Days}}
{{$.Charts.Lanes}}
{{$.Charts.Models}}
<table><tr><th>Role</th><th class="n">Turns</th><th class="n">Output</th><th class="n">Cost</th></tr>
{{range .ByRole}}<tr><td>{{.Key}}</td><td class="n">{{.Turns}}</td><td class="n">{{tok .Output}}</td><td class="n">{{usd .CostUSD}}</td></tr>
{{end}}</table>
<h3>Top sessions</h3>
<table><tr><th>Session</th><th class="n">Turns</th><th class="n">Output</th><th class="n">Cost</th></tr>
{{range .TopSessions}}<tr><td>{{.Key}}</td><td class="n">{{.Turns}}</td><td class="n">{{tok .Output}}</td><td class="n">{{usd .CostUSD}}</td></tr>
{{end}}</table>
<h3>aphrollo's injected text</h3>
<p>About {{tok .Injection.Tokens}} tokens{{if .Injection.InputShare}}, {{.Injection.InputShare}} of the input submitted{{end}}. Counted once when injected; the cache re-reads it on later turns.</p>
{{$.Charts.Injection}}
{{with .Compare}}<h3>Before and after {{.At}}</h3>
{{$.Charts.Compare}}
<table><tr><th></th><th class="n">Days</th><th class="n">Cost a day</th><th class="n">Output a day</th><th class="n">Injected share</th></tr>
{{range $.ComparePeriods}}<tr><td>{{.Name}}</td><td class="n">{{.Days}}</td><td class="n">{{usd .CostPerDay}}</td><td class="n">{{tok .OutputPerDay}}</td><td class="n">{{.Share}}</td></tr>
{{end}}</table>{{end}}
{{else}}<p class="none">no transcripts were read</p>{{end}}
</section>
</main>
</body>
</html>
`
