package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// execute runs the root command with args, capturing stdout+stderr, isolating
// state via a temp config dir so no real ~/.mdfly is touched.
func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("MDFLY_CONFIG_DIR", t.TempDir())
	root := NewRootCmd("test-1.2.3")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestHelpListsAllVerbs(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, verb := range []string{"publish", "update", "list", "delete", "remove", "open"} {
		if !strings.Contains(out, verb) {
			t.Errorf("--help output missing verb %q\n%s", verb, out)
		}
	}
}

func TestVersionPrints(t *testing.T) {
	out, _, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(out, "test-1.2.3") {
		t.Errorf("--version output = %q, want it to contain version", out)
	}
}

func TestStubVerbsReturnNotImplemented(t *testing.T) {
	for _, verb := range []string{"update", "list", "delete", "remove", "open"} {
		t.Run(verb, func(t *testing.T) {
			_, _, err := execute(t, verb)
			if !errors.Is(err, errNotImplemented) {
				t.Errorf("%s err = %v, want errNotImplemented", verb, err)
			}
		})
	}
}

func TestGlobalFlagsParseOnEveryVerb(t *testing.T) {
	for _, verb := range []string{"update", "list", "delete", "remove", "open"} {
		t.Run(verb, func(t *testing.T) {
			// Runtime error (not-implemented), but flag parsing must succeed.
			_, _, err := execute(t, "--json", "-v", "-q", "-y", "--no-update-check", "--api", "https://x.example", verb)
			if !errors.Is(err, errNotImplemented) {
				t.Errorf("%s with global flags err = %v, want errNotImplemented (flags should parse)", verb, err)
			}
		})
	}
}

func TestUnknownFlagIsParseError(t *testing.T) {
	_, _, err := execute(t, "list", "--nope")
	if err == nil {
		t.Fatal("unknown flag should error")
	}
	if errors.Is(err, errNotImplemented) {
		t.Fatal("unknown flag should fail at parse, not reach RunE")
	}
}

func TestUsageShownOnParseErrorNotRuntimeError(t *testing.T) {
	// Flag parsing fails before PersistentPreRunE flips SilenceUsage, so usage
	// is emitted. (Cobra routes it via OutOrStderr — the Out writer when set in
	// the harness; in the real binary Out is unset so it lands on stderr, see
	// the e2e TestCLIPublish_noArgs.)
	pOut, pErr, err := execute(t, "list", "--nope")
	if err == nil || !strings.Contains(pOut+pErr, "Usage:") {
		t.Errorf("flag-parse error should print usage; err=%v out=%q err=%q", err, pOut, pErr)
	}

	// A runtime RunE error runs after SilenceUsage is flipped, so no usage.
	rOut, rErr, err := execute(t, "list")
	if !errors.Is(err, errNotImplemented) {
		t.Fatalf("list err=%v, want errNotImplemented", err)
	}
	if strings.Contains(rOut+rErr, "Usage:") {
		t.Errorf("runtime RunE error should not print usage; out=%q err=%q", rOut, rErr)
	}
}

func TestNoBareFileShortcut(t *testing.T) {
	// A bare non-verb argument must be an unknown-command error, not dispatched.
	_, _, err := execute(t, "some-file.md")
	if err == nil {
		t.Fatal("bare file arg should error (no bare-command shortcut)")
	}
	if errors.Is(err, errNotImplemented) {
		t.Fatal("bare file arg should not reach a verb RunE")
	}
}
