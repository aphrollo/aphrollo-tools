level: patch

The red-to-green answer at the edit now fits its 50 ms on a busy box, so the A/B has decisions to count, and `aphrollo update` works from any directory.

### What you will notice

- A lane that did not commit while the repo's one event log grew re-read all of that growth at every code edit (about 45 ms idle and 125 ms on a loaded box, over a 20 MB log), so the hook dropped the question. The first read that finds a long unfolded tail now moves the lane's checkpoint past it when the lane's lock is free, and later edits read almost nothing (about 3 ms).
- The wait is sized from the decisions the hook recorded: p90 of their times, three times over, never under 50 ms and never over 500 ms, once 20 decisions are on record. A dropped decision counts at the time it had spent.
- A decision that is still dropped is written with the lane, the file, the seconds spent and the wait it was given.
- `aphrollo stats --ab` prints `N decisions unmeasured of M` per arm. When more than half of an arm's decisions were dropped, the verdict reads `deciding (decisions dropped)`, whatever the other metrics say.
- `aphrollo update` with no `--repo` builds from the checkout the last install came from (recorded beside the install), else from the current directory; when neither is the module it says to run it in your aphrollo-tools checkout or pass `--repo <path>`.
