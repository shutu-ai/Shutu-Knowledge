// Package operations owns durable, replayable long-running commands. HTTP
// callers submit versioned command payloads; workers claim rows from SQLite,
// so accepted work survives process restarts and response loss.
package operations

// State values form the state machine described by the architecture baseline.
const (
	StateQueued      = "queued"
	StateRunning     = "running"
	StateSucceeded   = "succeeded"
	StateFailed      = "failed"
	StateCancelled   = "cancelled"
	StateCancelling  = "cancelling"
	StateInterrupted = "interrupted"
)

// CommandSchemaV1 is the first stable input contract.
const CommandSchemaV1 = 1

// DefaultIdempotencyRetentionHours is the public terminal-response retry
// window. Zero means retain the binding indefinitely.
const DefaultIdempotencyRetentionHours = 168

// Resource classes are scheduler lanes. Existing commands are migrated to
// these finite budgets instead of competing in one unspecified worker pool.
const (
	ResourceIO          = "io"
	ResourceDBWrite     = "db_write"
	ResourceDisk        = "disk"
	ResourceNetwork     = "network"
	ResourceModel       = "model"
	ResourceMaintenance = "maintenance"
)

// Upload lifecycle states. A complete session becomes bound atomically with
// operation acceptance; bound input survives retries until final release.
const (
	UploadStateUploading = "uploading"
	UploadStateComplete  = "complete"
	UploadStateBound     = "bound"
	UploadStateReleased  = "released"
	UploadStateExpired   = "expired"
)
