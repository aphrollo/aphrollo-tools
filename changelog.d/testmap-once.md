level: patch

The background test-map build behind commit-time mutation no longer stacks: it starts only when trunk moves in the primary checkout, never on a merge or pull into a lane; one build per repository runs at a time, and requests that arrive meanwhile cost exactly one more pass; and it runs at below-normal priority.
