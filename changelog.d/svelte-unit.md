level: patch

A `.svelte` or `.vue` edit is opened by a red of its node package again. Since red-to-green moved to PreToolUse, a single-file component had no language row, so it got its own unit, and a vitest or jest red in the package never opened it: every component edit was refused as untested code. Components now share the TypeScript unit of their package, as `.js` files already did.
