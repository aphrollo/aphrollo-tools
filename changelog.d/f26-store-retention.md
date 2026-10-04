level: patch

`aphrollo gate gc` now also keeps the state directory within its retention, and `gate gc --dry` prints how big each part of it is.

### What you will notice

- Event log months older than 16 weeks, a removed lane's checkpoint 7 days after its removal, verdict files unused for 14 days (or all of whose lanes are closed, and past 2,000 files the least recently used) and the ratchet cache past 200 MB (least recently used first) are removed by the sweep the session start already runs once a day.
- A lane another process is committing to, every file of a live lane are left alone.
- `gate gc --dry` lists what it would remove and, per repo, the files and bytes of events, lanes, verdicts, jobs and out against their caps, then the same for the shared ratchet cache.
