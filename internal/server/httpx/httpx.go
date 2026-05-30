// Package httpx is the shared HTTP response toolkit for mdfly-server handlers.
// It owns the project-wide error envelope (CONTEXT.md "Error envelope") and
// the JSON response helper.
package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
)

// Error carries the HTTP status + machine-readable code + human message for a
// request that cannot be fulfilled. Handlers (and the workflow layer behind
// them) return *Error; WriteError serializes it in the project error envelope.
type Error struct {
	Status  int
	Code    string
	Msg     string
	Details any
}

func (e *Error) Error() string { return e.Msg }

func BadRequest(msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Msg: msg}
}

func NotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Msg: msg}
}

func Conflict(code, msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Msg: msg}
}

func Internal(msg string) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error", Msg: msg}
}

// Unprocessable returns a 422 error with a custom machine-readable code and an
// empty details object (ADR-0013 idempotency payload mismatch).
func Unprocessable(code, msg string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: code, Msg: msg, Details: map[string]any{}}
}

// BundleTooLarge returns a 413 error for a bundle limit violation.
func BundleTooLarge(limitName string, max, actual int64) *Error {
	return &Error{
		Status: http.StatusRequestEntityTooLarge,
		Code:   "bundle_too_big",
		Msg:    fmt.Sprintf("bundle exceeds %s limit (%d, max %d)", limitName, actual, max),
		Details: map[string]any{
			"limit":  limitName,
			"max":    max,
			"actual": actual,
		},
	}
}

// WriteJSON writes v as the response body with the given status and the
// application/json content type.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// WriteError serializes e in the project error envelope.
func WriteError(w http.ResponseWriter, e *Error) {
	WriteJSON(w, e.Status, api.ErrorResponse{
		Error: api.ErrorBody{Code: e.Code, Message: e.Msg, Details: e.Details},
	})
}
