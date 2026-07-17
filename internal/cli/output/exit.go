// Package output owns the CLI's error-to-user pipeline: the central
// error→exit-code mapping and the rendering of errors to the terminal.
// stdout carries only the canonical artifact (URL / --json object); stderr
// carries all narration. main is the sole caller of os.Exit.
package output

import (
	"errors"
	"io/fs"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/publish"
)

// Categorical process exit codes (PRD CLI Anon-Tier §error taxonomy).
const (
	ExitOK           = 0 // success
	ExitUnclassified = 1 // unrecognized failure
	ExitUsage        = 2 // input: bad flag/arg, no content, client-side too big, 400, 422
	ExitAuth         = 3 // 401/403 bad or missing Edit Token
	ExitConflict     = 4 // 409 slug taken / update conflict
	ExitQuota        = 5 // 413 server too big / 429 rate limit
	ExitServer       = 6 // 5xx / network / PUT-fail-after-retries
)

// UsageError marks a client-side input mistake (bad flag, missing arg, empty
// content, unreadable file) that maps to ExitUsage.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// ExitCode maps a command error to a process exit code. It classifies typed
// local errors first, then server APIErrors on their machine code, falling
// back to HTTP status class.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return ExitUsage
	}
	var contentErr *input.ContentError
	if errors.As(err, &contentErr) {
		return ExitUsage
	}
	var limitErr *publish.BundleLimitError
	if errors.As(err, &limitErr) {
		return ExitUsage
	}
	var apiErr *publish.APIError
	if errors.As(err, &apiErr) {
		return exitForAPI(apiErr)
	}
	var transErr *publish.TransientError
	if errors.As(err, &transErr) {
		return ExitServer
	}
	// A non-existent or unreadable file argument is an input error.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return ExitUsage
	}
	return ExitUnclassified
}

// exitForAPI maps a server error, preferring the machine code over the status.
func exitForAPI(e *publish.APIError) int {
	switch e.Code {
	case api.CodeBadBundle, api.CodeIdempotencyPayloadMismatch:
		return ExitUsage
	case api.CodeInvalidEditToken:
		return ExitAuth
	case api.CodeSlugTaken, api.CodeUpdateConflict:
		return ExitConflict
	case api.CodeBundleTooLarge, api.CodeRateLimited:
		return ExitQuota
	}
	return exitForStatus(e.Status)
}

func exitForStatus(status int) int {
	switch {
	case status == http.StatusBadRequest, status == http.StatusUnprocessableEntity:
		return ExitUsage
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ExitAuth
	case status == http.StatusConflict:
		return ExitConflict
	case status == http.StatusRequestEntityTooLarge, status == http.StatusTooManyRequests:
		return ExitQuota
	case status >= 500:
		return ExitServer
	}
	return ExitUnclassified
}
