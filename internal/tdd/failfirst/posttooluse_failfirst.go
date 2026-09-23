package failfirst

import "time"

// DefaultPrecommitTimeout is the canonical Precommit/Mechanical stage
// budget — the single source of truth for cli.go's precommitTimeout.
const DefaultPrecommitTimeout = 600 * time.Second
