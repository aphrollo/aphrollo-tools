level: minor

The text aphrollo puts in front of every session is smaller, and a small request no longer starts a lane, a builder and a review.

### What you will notice

- The managed CLAUDE.md block now says there are two modes. Questions, checks, analysis, fixes and small features are done by the session itself with no subagents, reviewer or plan; an edit goes in a lane the session makes and merges. Only `/sdd`, or an explicit ask for a plan, brings a builder per lane and a cold reviewer. The builder and reviewer agents say they are for planned work.
- Two lines the gate used to inject unasked are off by default: the post-merge retro questions, and the session-start line with the open issue and escape counts (the weekly digest drops its open-escape clause with it). Set `retro-prompt = true` and `issue-prompt = true` in `aphrollo.toml`, `trellis.toml` or your user `config.toml` to get them back unchanged. Escape records and `aphrollo gate stats` are untouched.
- The managed block, the tdd skill and the three agents are cut to their token caps (400 for the block and the skill, 250 for each agent), and a test now fails if any of them grows past its cap. Run `aphrollo install --managed-block-only --repo <lane>` in a lane to refresh a committed block.
