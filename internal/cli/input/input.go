// Package input resolves where a publish's content comes from before any
// bundling happens: a positional file, inline -m/--message text, or piped/
// redirected stdin. These origins are mutually exclusive; the module rejects
// ambiguous, empty, or unresolvable input early with an exit-2 ContentError.
package input

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Kind names the resolved content origin.
type Kind int

const (
	KindFile Kind = iota + 1 // a positional file on disk
	KindText                 // inline -m text or piped/redirected stdin (a Text Publish)
)

// Source is the resolved content origin. Path is set for KindFile; Content
// holds the raw bytes for KindText.
type Source struct {
	Kind    Kind
	Path    string
	Content []byte
}

// Request carries the raw origins a verb collected from flags and args. Stdin
// and StdinIsTTY are supplied by the caller so resolution stays testable
// without a real terminal.
type Request struct {
	File       string    // positional path arg, "" if none
	Message    string    // -m/--message value
	MessageSet bool      // whether -m/--message was provided
	Stdin      io.Reader // stdin, read only when it is the resolved source
	StdinIsTTY bool      // isatty(stdin): true means no piped/redirected input
}

// Resolve selects the single content source. Precedence: a positional file and
// -m are mutually exclusive; a file wins over stdin; -m wins over stdin; piped
// stdin is the fallback. With none present it returns a "nothing to publish"
// ContentError.
func Resolve(req Request) (Source, error) {
	if req.File != "" && req.MessageSet {
		return Source{}, newContentError("-m/--message and a file argument are mutually exclusive")
	}
	switch {
	case req.File != "":
		return resolveFile(req.File)
	case req.MessageSet:
		return resolveText(req.Message)
	case !req.StdinIsTTY:
		return resolveStdin(req.Stdin)
	default:
		return Source{}, newContentError("nothing to publish")
	}
}

// resolveFile validates that a positional path exists and, for a regular file,
// is readable and non-empty. Directories are left for the walk to expand.
func resolveFile(path string) (Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Source{}, &ContentError{Err: err}
	}
	if info.Mode().IsRegular() {
		if info.Size() == 0 {
			return Source{}, newContentError(fmt.Sprintf("file is empty: %s", path))
		}
		f, err := os.Open(path)
		if err != nil {
			return Source{}, &ContentError{Err: err}
		}
		f.Close()
	}
	return Source{Kind: KindFile, Path: path}, nil
}

func resolveText(msg string) (Source, error) {
	if strings.TrimSpace(msg) == "" {
		return Source{}, newContentError("-m/--message is empty")
	}
	return Source{Kind: KindText, Content: []byte(msg)}, nil
}

func resolveStdin(r io.Reader) (Source, error) {
	if r == nil {
		return Source{}, newContentError("nothing to publish")
	}
	content, err := io.ReadAll(r)
	if err != nil {
		return Source{}, &ContentError{Err: err}
	}
	if strings.TrimSpace(string(content)) == "" {
		return Source{}, newContentError("stdin is empty")
	}
	return Source{Kind: KindText, Content: content}, nil
}

// IsTerminal reports whether f is an interactive terminal, used to distinguish
// piped/redirected stdin (source present) from an interactive TTY (absent).
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
