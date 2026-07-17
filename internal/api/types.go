package api

// Machine-readable error codes carried in the error envelope's `code` field.
// The enum is shared so the server's envelope and the CLI's exit-code mapping
// cannot drift. The CLI maps on these codes first, falling back to HTTP status.
const (
	// CodeIdempotencyPayloadMismatch is returned by /v1/publish/init (422) when
	// an idempotency_key is reused with a different payload (ADR-0013).
	CodeIdempotencyPayloadMismatch = "idempotency_key_payload_mismatch"
	// CodeBadBundle is returned (400) when the submitted bundle is malformed.
	CodeBadBundle = "bad_bundle"
	// CodeInvalidEditToken is returned (401/403) when the Edit Token is missing
	// or does not match the document (ADR-0015).
	CodeInvalidEditToken = "invalid_edit_token"
	// CodeSlugTaken is returned (409) when the requested slug is already in use.
	CodeSlugTaken = "slug_taken"
	// CodeUpdateConflict is returned (409) when a concurrent update lost the
	// optimistic-concurrency check (ADR-0012).
	CodeUpdateConflict = "update_conflict"
	// CodeBundleTooLarge is returned (413) when the bundle exceeds the server's
	// tier quota.
	CodeBundleTooLarge = "bundle_too_large"
	// CodeRateLimited is returned (429) when the client is rate limited.
	CodeRateLimited = "rate_limited"
)

// BundleFileDTO is one file entry in a BundleDTO, sent over the wire at init.
type BundleFileDTO struct {
	Path string `json:"path"`
	Hash string `json:"hash"` // hex-encoded SHA256 of the file content
	Size int64  `json:"size"`
}

// BundleDTO is the bundle description the CLI computes and sends at init.
// RootPath is the project-root-relative path of the root markdown file within
// Files. ProjectRoot is the publisher's absolute project-root path, used by the
// server to resolve absolute references in markdown content.
type BundleDTO struct {
	RootPath    string          `json:"root_path"`
	ProjectRoot string          `json:"project_root"`
	Files       []BundleFileDTO `json:"files"`
}

// InitRequest is the request body for POST /v1/publish/init.
type InitRequest struct {
	IdempotencyKey string    `json:"idempotency_key"`
	Bundle         BundleDTO `json:"bundle"`
	EditToken      string    `json:"edit_token,omitempty"`
}

// InitResponse is the body of a successful POST /v1/publish/init.
// PresignedURLs maps a representative bundle path to its presigned PUT URL.
// The server deduplicates by R2 blobkey (slug+hash+ext), so paths that share
// a blobkey with a representative are not in the map — uploading the
// representative covers their content. The CLI iterates this map and PUTs
// each (path, url) pair using the bundle file at that path.
type InitResponse struct {
	Slug          string            `json:"slug"`
	PresignedURLs map[string]string `json:"presigned_urls"`
	InlineAccept  bool              `json:"inline_accept"`
}

// CommitRequest is the request body for POST /v1/publish/commit.
type CommitRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

// CommitResponse is the body of a successful POST /v1/publish/commit.
type CommitResponse struct {
	URL          string `json:"url"`
	Slug         string `json:"slug"`
	ManifestHash string `json:"manifest_hash"` // hex-encoded SHA256 of manifest JSON
}

// ErrorResponse is the error envelope for all 4xx/5xx responses.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the machine-readable error code + human message.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}
