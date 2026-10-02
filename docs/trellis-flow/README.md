# trellis session flow

One diagram of a whole Claude Code session with trellis on top: Claude Code's
hook lifecycle, with every trellis flow drawn at the hook that runs it. trellis
is the plugin that carries this repo's gates (and its `trellis` command).

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="session-dark.svg">
  <img alt="Claude Code's hook lifecycle with trellis on top: one session" src="session-light.svg">
</picture>

## How to read it

- A pill is a start or an end state; a blue pill is a hand-over, named "X ready".
- A diamond is the only place a path splits; every branch is labelled.
- A box is a step; a dotted box runs outside Claude Code.
- A dashed box on the left is a Claude Code side event.
- A dashed frame groups one trellis flow; the blue frame is each turn, the orange one the agentic loop.
- Two joins hold the loop together: every call returns through "Tool succeeded?",
  and every orange line goes back to "Claude calls a tool?".

## Changing it

The diagram is generated; never edit an SVG by hand.

- `session_flow.py` is the flow itself, top to bottom: hooks, boxes, diamonds
  and where each branch goes.
- `engine.py` lays it out: it places every item, wraps the labels and routes
  every line.
- `build.py` writes `session.svg`, `session-light.svg`, `session-dark.svg` and
  `workbench.html`:

```
python3 docs/trellis-flow/build.py
```

Open `workbench.html` in a browser to zoom and pan, and to run the layout
check: overlapping shapes, text over an edge, a line through a shape or a
label, and an arrow shorter than its label. Commit the flow only when the check
is clean.
