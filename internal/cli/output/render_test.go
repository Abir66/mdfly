package output

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/publish"
)

func render(t *testing.T, err error, opts Options) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	RenderError(&out, &errOut, err, opts)
	return out.String(), errOut.String()
}

// TestRenderError_humanMessageAndHint shows the server's human message plus a
// hint for a common case, and hides the machine code + HTTP status.
func TestRenderError_humanMessageAndHint(t *testing.T) {
	err := &publish.APIError{Status: http.StatusConflict, Code: api.CodeSlugTaken, Message: "slug 'notes' is already taken"}
	out, errOut := render(t, err, Options{})

	if out != "" {
		t.Errorf("stdout=%q, want empty (human errors go to stderr)", out)
	}
	if !strings.Contains(errOut, "slug 'notes' is already taken") {
		t.Errorf("stderr missing message: %q", errOut)
	}
	if !strings.Contains(errOut, "--slug") {
		t.Errorf("stderr missing hint for slug_taken: %q", errOut)
	}
	if strings.Contains(errOut, api.CodeSlugTaken) || strings.Contains(errOut, "409") {
		t.Errorf("machine code/status leaked without -v: %q", errOut)
	}
}

// TestRenderError_verboseRevealsMachineDetail asserts -v surfaces the machine
// code and HTTP status.
func TestRenderError_verboseRevealsMachineDetail(t *testing.T) {
	err := &publish.APIError{Status: http.StatusConflict, Code: api.CodeSlugTaken, Message: "slug taken"}
	_, errOut := render(t, err, Options{Verbose: true})

	if !strings.Contains(errOut, api.CodeSlugTaken) {
		t.Errorf("verbose stderr missing machine code: %q", errOut)
	}
	if !strings.Contains(errOut, "409") {
		t.Errorf("verbose stderr missing HTTP status: %q", errOut)
	}
}

// TestRenderError_jsonShape asserts the {error:{code,message,exit}} envelope on
// stdout only.
func TestRenderError_jsonShape(t *testing.T) {
	err := &publish.APIError{Status: http.StatusConflict, Code: api.CodeSlugTaken, Message: "slug taken"}
	out, errOut := render(t, err, Options{JSON: true})

	if errOut != "" {
		t.Errorf("stderr=%q, want empty (--json error goes to stdout)", errOut)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Exit    int    `json:"exit"`
		} `json:"error"`
	}
	if e := json.Unmarshal([]byte(out), &env); e != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", e, out)
	}
	if env.Error.Code != api.CodeSlugTaken {
		t.Errorf("json code=%q, want %q", env.Error.Code, api.CodeSlugTaken)
	}
	if env.Error.Message != "slug taken" {
		t.Errorf("json message=%q", env.Error.Message)
	}
	if env.Error.Exit != ExitConflict {
		t.Errorf("json exit=%d, want %d", env.Error.Exit, ExitConflict)
	}
}

// TestRenderError_localErrorNoCode renders a plain local error message with no
// machine code.
func TestRenderError_localErrorNoCode(t *testing.T) {
	err := &UsageError{Err: errNoContent()}
	out, errOut := render(t, err, Options{})
	if out != "" {
		t.Errorf("stdout=%q, want empty", out)
	}
	if !strings.Contains(errOut, "no content") {
		t.Errorf("stderr missing message: %q", errOut)
	}
}

// TestRenderError_noColorToBuffer asserts no ANSI escape codes are emitted when
// the writer is not a TTY.
func TestRenderError_noColorToBuffer(t *testing.T) {
	err := &publish.APIError{Status: http.StatusConflict, Code: api.CodeSlugTaken, Message: "slug taken"}
	_, errOut := render(t, err, Options{})
	if strings.Contains(errOut, "\x1b") {
		t.Errorf("ANSI escape emitted to non-TTY buffer: %q", errOut)
	}
}

func errNoContent() error { return &staticErr{"no content to publish"} }

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }
