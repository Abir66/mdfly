package input

import "errors"

// ContentError marks an input mistake in the content source — ambiguous origin,
// empty content, or an absent/unreadable file. It maps to exit code 2.
type ContentError struct{ Err error }

func (e *ContentError) Error() string { return e.Err.Error() }
func (e *ContentError) Unwrap() error { return e.Err }

func newContentError(msg string) *ContentError {
	return &ContentError{Err: errors.New(msg)}
}
