package api

// ManifestFile is one entry in a publish bundle.
type ManifestFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"` // hex-encoded SHA256
	Size int64  `json:"size"`
}

// Manifest is the bundle description the CLI computes and sends at init.
type Manifest struct {
	Root  string         `json:"root"`
	Files []ManifestFile `json:"files"`
}

// InitRequest is the request body for POST /v1/publish/init.
type InitRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	Manifest       Manifest `json:"manifest"`
	EditToken      string   `json:"edit_token,omitempty"`
}

// InitResponse is the body of a successful POST /v1/publish/init.
type InitResponse struct {
	Slug          string            `json:"slug"`
	MissingHashes []string          `json:"missing_hashes"`
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
}
