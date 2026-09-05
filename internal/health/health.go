// Package health tracks subsystem status: process, database, index
// subsystems, and optional model availability. Optional components degrade
// instead of failing readiness unless configured as required.
package health

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Severity distinguishes readiness-critical checks from degradable ones.
type Severity int

const (
	// Critical checks fail readiness (process, database, index).
	Critical Severity = iota
	// Optional checks never fail readiness; they surface in detail.
	Optional
)

// Checker is one named subsystem probe.
type Checker interface {
	Name() string
	Severity() Severity
	Check(ctx context.Context) error
}

// CheckerFunc adapts a function into a Checker.
type CheckerFunc struct {
	CheckName string
	Level     Severity
	Fn        func(ctx context.Context) error
}

// Name implements Checker.
func (f CheckerFunc) Name() string { return f.CheckName }

// Severity implements Checker.
func (f CheckerFunc) Severity() Severity { return f.Level }

// Check implements Checker.
func (f CheckerFunc) Check(ctx context.Context) error { return f.Fn(ctx) }

// Registry aggregates checkers.
type Registry struct {
	mu       sync.RWMutex
	checkers []Checker
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a checker.
func (r *Registry) Register(c Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers = append(r.checkers, c)
}

// Component is one subsystem's observed state.
type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | degraded | failed
	Detail string `json:"detail,omitempty"`
}

// Report is the aggregated health snapshot.
type Report struct {
	Ready      bool        `json:"ready"`
	Status     string      `json:"status"`
	Components []Component `json:"components"`
}

// Snapshot runs all checkers and aggregates readiness. A critical failure
// flips Ready to false; an optional failure marks that component degraded
// but keeps the overall service ready (unless configured required, which the
// caller expresses by registering the checker as Critical).
func (r *Registry) Snapshot(ctx context.Context) Report {
	r.mu.RLock()
	checkers := make([]Checker, len(r.checkers))
	copy(checkers, r.checkers)
	r.mu.RUnlock()

	report := Report{Ready: true, Status: "ready"}
	for _, checker := range checkers {
		component := Component{Name: checker.Name(), Status: "ok"}
		if err := checker.Check(ctx); err != nil {
			if checker.Severity() == Critical {
				component.Status = "failed"
				report.Ready = false
			} else {
				component.Status = "degraded"
			}
			component.Detail = err.Error()
		}
		report.Components = append(report.Components, component)
	}
	sort.Slice(report.Components, func(i, j int) bool { return report.Components[i].Name < report.Components[j].Name })
	if !report.Ready {
		var failed []string
		for _, c := range report.Components {
			if c.Status == "failed" {
				failed = append(failed, c.Name)
			}
		}
		report.Status = "unhealthy: " + strings.Join(failed, ",")
	}
	return report
}

// StaticChecker returns a checker with a fixed outcome (tests, doctor).
func StaticChecker(name string, level Severity, err error) Checker {
	return CheckerFunc{CheckName: name, Level: level, Fn: func(context.Context) error {
		if err != nil {
			return fmt.Errorf("%s", err)
		}
		return nil
	}}
}
