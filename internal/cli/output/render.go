package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/publish"
)

const (
	colorReset = "\x1b[0m"
	colorRed   = "\x1b[31m"
	colorDim   = "\x1b[2m"
)

// hints maps a machine error code to an actionable one-line hint.
var hints = map[string]string{
	api.CodeSlugTaken:                  "pick another slug with --slug.",
	api.CodeUpdateConflict:             "someone else updated it first; re-fetch and retry.",
	api.CodeInvalidEditToken:           "you may not own this document, or its edit token is stale.",
	api.CodeBundleTooLarge:             "reduce the bundle size or the number of files.",
	api.CodeRateLimited:                "you are being rate limited; wait a moment and retry.",
	api.CodeIdempotencyPayloadMismatch: "usually a state.json or scripting bug — the same key was reused with a different payload.",
}

// Options controls how RenderError renders.
type Options struct {
	JSON    bool // emit the machine-readable {error:{...}} envelope on stdout
	Verbose bool // reveal the machine code + HTTP status on stderr
}

// RenderError writes err for the user. In JSON mode it emits the canonical
// {error:{code,message,exit}} object on stdout; otherwise it writes the human
// message (+ hint, + machine detail under Verbose) on stderr.
func RenderError(stdout, stderr io.Writer, err error, opts Options) {
	if err == nil {
		return
	}
	code, status, message := describe(err)
	exit := ExitCode(err)

	if opts.JSON {
		renderJSON(stdout, code, message, exit)
		return
	}
	renderHuman(stderr, code, status, message, opts.Verbose)
}

func renderJSON(w io.Writer, code, message string, exit int) {
	type body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Exit    int    `json:"exit"`
	}
	enc := json.NewEncoder(w)
	enc.Encode(struct { //nolint:errcheck
		Error body `json:"error"`
	}{Error: body{Code: code, Message: message, Exit: exit}})
}

func renderHuman(w io.Writer, code string, status int, message string, verbose bool) {
	color := useColor(w)
	fmt.Fprintln(w, paint(color, colorRed, message))
	if h := hints[code]; h != "" {
		fmt.Fprintln(w, paint(color, colorDim, "hint: "+h))
	}
	if verbose {
		fmt.Fprintln(w, paint(color, colorDim, fmt.Sprintf("(code=%s status=%d)", codeOrDash(code), status)))
	}
}

// describe extracts the machine code, HTTP status, and human message from err.
func describe(err error) (code string, status int, message string) {
	var apiErr *publish.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code, apiErr.Status, apiErr.Error()
	}
	return "", 0, err.Error()
}

func codeOrDash(code string) string {
	if code == "" {
		return "-"
	}
	return code
}

func paint(enabled bool, color, s string) string {
	if !enabled {
		return s
	}
	return color + s + colorReset
}

// useColor reports whether ANSI color is appropriate for w: only when NO_COLOR
// is unset and w is a terminal.
func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
