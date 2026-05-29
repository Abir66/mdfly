package publish

import (
	"fmt"

	"github.com/Abir66/mdfly/internal/api"
)

// BundleLimitError is returned by checkBundleLimits when the bundle exceeds
// an Anonymous-tier limit. The CLI exits with code 2 on this error type.
type BundleLimitError struct {
	Limit  string // "total_bytes", "file_count", "single_file_bytes"
	Max    int64
	Actual int64
}

func (e *BundleLimitError) Error() string {
	return fmt.Sprintf("bundle limit exceeded: %s %d (max %d)", e.Limit, e.Actual, e.Max)
}

func checkBundleLimits(b Bundle) error {
	if len(b.FilesByPath) > api.AnonMaxFileCount {
		return &BundleLimitError{
			Limit:  "file_count",
			Max:    int64(api.AnonMaxFileCount),
			Actual: int64(len(b.FilesByPath)),
		}
	}
	var total int64
	for _, f := range b.FilesByPath {
		if f.Size > api.AnonMaxSingleFileBytes {
			return &BundleLimitError{
				Limit:  "single_file_bytes",
				Max:    api.AnonMaxSingleFileBytes,
				Actual: f.Size,
			}
		}
		total += f.Size
	}
	if total > api.AnonMaxTotalBytes {
		return &BundleLimitError{
			Limit:  "total_bytes",
			Max:    api.AnonMaxTotalBytes,
			Actual: total,
		}
	}
	return nil
}
