// Package filetree is the pure, I/O-free resolution core for the Viewer chrome.
// It consumes the set of project-root-relative Bundle Manifest keys and a request
// path and decides what the path addresses. project_root never enters this module
// — that purity is the structural privacy guarantee (PRD: Viewer Chrome, ADR-0024).
package filetree

import "strings"

// Kind is the category of node a request path resolves to.
type Kind int

const (
	// NotFound means the path matches neither a manifest key nor a directory prefix.
	NotFound Kind = iota
	// File means the path is an exact manifest key.
	File
	// Dir means the path is a directory prefix shared by one or more keys.
	Dir
)

// Result is the outcome of Classify. For File, Key is the exact manifest key; for
// Dir, Key is the normalized directory prefix (no leading/trailing slash).
type Result struct {
	Kind Kind
	Key  string
}

// Classify resolves reqPath against the manifest keys. An exact key match wins
// over a directory prefix (so a file and a same-stem folder can coexist). A path
// that is the prefix of some key (or the empty root) is a directory; anything
// else is NotFound. Leading and trailing slashes are ignored.
func Classify(keys []string, reqPath string) Result {
	p := strings.Trim(reqPath, "/")
	for _, k := range keys {
		if k == p {
			return Result{Kind: File, Key: k}
		}
	}
	if p == "" {
		return Result{Kind: Dir, Key: ""}
	}
	prefix := p + "/"
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return Result{Kind: Dir, Key: p}
		}
	}
	return Result{Kind: NotFound}
}
