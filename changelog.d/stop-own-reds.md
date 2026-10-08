level: patch

A stop now judges only the reds of the agent that is stopping. A subagent's hooks carry its session's id, so a session's stop used to count a builder's deliberate red in its own lane. Under `tdd = warn` that printed a warning for a red that was not the session's, and under `tdd = enforce` it would have blocked the session's turn. Each deferred run now records the agent whose edit started it. A Stop judges the runs the session's own agent started, and a SubagentStop judges its own runs and those recorded before runs named their agent.

### What you will notice

The `tdd = warn` stop message names each red's checkout and the command that shows its output.
