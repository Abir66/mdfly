package api

// BundleFileDTO is one file entry in a BundleDTO, sent over the wire at init.
type BundleFileDTO struct {
	Path string `json:"path"`
	Hash string `json:"hash"` // hex-encoded SHA256 of the file content
	Size int64  `json:"size"`
}

// BundleDTO is the bundle description the CLI computes and sends at init.
// RootPath is the logical path of the root markdown file within Files.
type BundleDTO struct {
	RootPath string          `json:"root_path"`
	Files    []BundleFileDTO `json:"files"`
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
}
