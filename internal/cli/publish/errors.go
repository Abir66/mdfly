package publish

// idempotencyMismatchMsg is the user-facing explanation printed when the server
// rejects init with 422 because the idempotency_key was reused with a different
// payload (ADR-0013 / S15). The CLI exits with code 2 on this error.
const idempotencyMismatchMsg = "idempotency key collision with a different payload — usually means a state.json or scripting bug"

// IdempotencyMismatchError is returned when /v1/publish/init responds 422 with
// the idempotency_key_payload_mismatch code. The CLI exits with code 2.
type IdempotencyMismatchError struct{}

func (e *IdempotencyMismatchError) Error() string { return idempotencyMismatchMsg }
