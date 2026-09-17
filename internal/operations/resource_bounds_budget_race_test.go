//go:build race

package operations

import "time"

// The race detector instruments every SQLite and scheduler synchronization
// point. Keep the test bounded while allowing that development-only overhead;
// the normal build remains governed by the production three-second budget.
const resourceDrainBudget = 10 * time.Second
