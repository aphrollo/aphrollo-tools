level: patch

The merge queue now reuses a green pull request run as intended. `aphrollo ci reuse` read the sha in a queue branch name (`gh-readonly-queue/main/pr-<n>-<sha>`) as the pull request's head, but it is the group's base, so every queue run refused with "it moved" and reran the full suite. The sha is now checked against the group's base. A pull request that moved after it was queued is still refused, because its newest run tested another tree.
