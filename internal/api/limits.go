package api

// Anonymous-tier bundle limits (CONTEXT.md [[Bundle Limits]]).
// Change here propagates to both CLI preflight and server re-validation.
const (
	AnonMaxTotalBytes      int64 = 25 * 1024 * 1024 // 25 MB
	AnonMaxFileCount       int   = 50
	AnonMaxSingleFileBytes int64 = 10 * 1024 * 1024 // 10 MB
)
