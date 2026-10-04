level: patch

`aphrollo gate gc` now also keeps the state directory within its retention, and `gate gc --dry` prints how big each part of it is.

### What you will notice

- Event log months older than 16 weeks, a removed lane's checkpoint 7 days after its removal, verdict files unused for 14 days (or all of whose lanes are closed, and past 2,000 files the least recently used) and the ratchet cache past 200 MB (least recently used first) are removed by the sweep the session start already runs once a day.
- An event month that holds an event of a lane that is not removed stays, so a lane can always be rebuilt from the log; the first removal leaves an `events-swept-through` marker. `stats` and `why` print "events since YYYY-MM" when months were swept, and `workspace sync --since` and the post-merge hook treat a merge from before the retained log as unknown rather than recording it as an outside merge and an escape.
- The ratchet cache files a run reads have their time renewed, so the least-recently-used cap evicts cold ones first.
- `gate gc --dry` creates nothing.
- A lane another process is committing to, every file of a live lane are left alone.
- `gate gc --dry` lists what it would remove and, per repo, the files and bytes of events, lanes, verdicts, jobs and out against their caps, then the same for the shared ratchet cache.
