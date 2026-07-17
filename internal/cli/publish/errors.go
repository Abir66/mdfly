package publish

import "fmt"

// APIError is a parsed error envelope from the backend. It carries the HTTP
// status alongside the machine-readable code, human message, and optional
// details so the CLI can map on the code first and fall back to status class.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("server returned status %d", e.Status)
}
