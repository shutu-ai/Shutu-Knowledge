//go:build !race

package operations

import "time"

// The production-facing resource budget remains three seconds. Race builds
// use a separate diagnostic budget in resource_bounds_budget_race_test.go so
// detector overhead cannot turn an otherwise bounded stress test into a false
// production-SLO failure.
const resourceDrainBudget = 3 * time.Second
