// Package blobmeta defines the response metadata stored on every R2 object at
// PUT time — the content type and cache policy storage.mdfly.dev serves back.
//
// Both sides of the upload import it. The server signs these values into the
// presigned PUT and the CLI sends them as headers, so the two must derive
// byte-identical strings from the same logical path: a mismatch is a 403
// SignatureDoesNotMatch, not a wrong header.
package blobmeta

import (
	"path/filepath"
	"strings"
)

// CacheControl is stored on every blob. Keys are content-addressed, so a blob's
// bytes never change and the value can be immutable (CONTEXT.md "Edge").
const CacheControl = "public, max-age=31536000, immutable"

// DefaultContentType is stored for any extension outside contentTypes. It makes
// the browser download rather than render, the safe default for arbitrary bytes.
const DefaultContentType = "application/octet-stream"

// contentTypes maps a lowercased extension to the content type stored on the
// blob. Only formats a rendered page embeds are listed — the browser refuses to
// content-sniff SVG in <img> and needs the exact type, and sniffs the raster
// formats only because they carry magic bytes. Everything else downloads.
//
// This must NOT be merged with the /raw/ surface's inlineTypes. That path serves
// SVG as text/plain because it answers at an mdfly.dev URL, where an active
// document would run script on the apex origin. A blob answers at
// storage.mdfly.dev, a separate origin whose direct-visit script is neutered by
// a Content-Security-Policy: sandbox edge rule (docs/deploy/3-cloudflare.md).
// Text and markup types are absent on purpose: no extension may map to
// text/html here.
var contentTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
}

// ContentType returns the content type stored for a logical path or blob key.
// Both name the same file and end in the same extension, so either resolves to
// the same value.
func ContentType(path string) string {
	if ct, ok := contentTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return ct
	}
	return DefaultContentType
}
