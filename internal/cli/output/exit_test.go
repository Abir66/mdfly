package output

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/publish"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, ExitOK},
		{"usage error", &UsageError{Err: errors.New("missing arg")}, ExitUsage},
		{"client-side too big", &publish.BundleLimitError{Limit: "total_bytes", Max: 1, Actual: 2}, ExitUsage},

		{"400 bad bundle status", &publish.APIError{Status: http.StatusBadRequest}, ExitUsage},
		{"400 bad bundle code", &publish.APIError{Status: http.StatusBadRequest, Code: api.CodeBadBundle}, ExitUsage},
		{"422 idempotency mismatch", &publish.APIError{Status: http.StatusUnprocessableEntity, Code: api.CodeIdempotencyPayloadMismatch}, ExitUsage},

		{"401 auth", &publish.APIError{Status: http.StatusUnauthorized}, ExitAuth},
		{"403 auth", &publish.APIError{Status: http.StatusForbidden}, ExitAuth},
		{"invalid edit token code", &publish.APIError{Status: http.StatusForbidden, Code: api.CodeInvalidEditToken}, ExitAuth},

		{"409 slug taken code", &publish.APIError{Status: http.StatusConflict, Code: api.CodeSlugTaken}, ExitConflict},
		{"409 update conflict code", &publish.APIError{Status: http.StatusConflict, Code: api.CodeUpdateConflict}, ExitConflict},
		{"409 status fallback", &publish.APIError{Status: http.StatusConflict}, ExitConflict},

		{"413 server too big", &publish.APIError{Status: http.StatusRequestEntityTooLarge}, ExitQuota},
		{"413 too large code", &publish.APIError{Status: http.StatusRequestEntityTooLarge, Code: api.CodeBundleTooLarge}, ExitQuota},
		{"429 rate limit", &publish.APIError{Status: http.StatusTooManyRequests}, ExitQuota},
		{"429 rate limit code", &publish.APIError{Status: http.StatusTooManyRequests, Code: api.CodeRateLimited}, ExitQuota},

		{"500 server", &publish.APIError{Status: http.StatusInternalServerError}, ExitServer},
		{"503 server", &publish.APIError{Status: http.StatusServiceUnavailable}, ExitServer},
		{"transient network", &publish.TransientError{}, ExitServer},

		{"file not found", fmt.Errorf("build bundle: %w", fs.ErrNotExist), ExitUsage},
		{"file unreadable", fmt.Errorf("read: %w", fs.ErrPermission), ExitUsage},

		{"unclassified", errors.New("boom"), ExitUnclassified},
		{"wrapped api error", fmt.Errorf("publish/init: %w", &publish.APIError{Status: http.StatusConflict}), ExitConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCode(c.err); got != c.want {
				t.Errorf("ExitCode(%v)=%d, want %d", c.err, got, c.want)
			}
		})
	}
}

// TestExitCode_clientVsServerTooBig locks the deliberate split: a client-side
// bundle-limit failure is input (2); a server 413 is quota (5).
func TestExitCode_clientVsServerTooBig(t *testing.T) {
	client := ExitCode(&publish.BundleLimitError{Limit: "total_bytes", Max: 1, Actual: 2})
	server := ExitCode(&publish.APIError{Status: http.StatusRequestEntityTooLarge})
	if client != ExitUsage {
		t.Errorf("client-side too big=%d, want %d", client, ExitUsage)
	}
	if server != ExitQuota {
		t.Errorf("server 413=%d, want %d", server, ExitQuota)
	}
}
