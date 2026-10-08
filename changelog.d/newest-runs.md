level: minor

A binary older than the user-space install now hands its work to it.

### What you will notice

- A stale `aphrollo` (for example a root-owned `/usr/local/bin/aphrollo`) that is asked for a `workspace`, `ratchet`, `ci` or `gate` hook verb runs the strictly newer user-space `current` instead, after one stderr line naming both versions.
- `aphrollo version` prints `newer install: <ver> at <path>` when the user-space install is newer than the binary running.
- `APHROLLO_NO_HANDOFF=1` turns the handoff off. Anything unreadable, equal or older runs the binary you called, silently. Only binaries from this release on can hand off.
