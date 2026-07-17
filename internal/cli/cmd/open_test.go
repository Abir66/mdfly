package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/cli/output"
	"github.com/spf13/cobra"
)

var errLaunch = errors.New("no browser")

// stubLauncher swaps browserLauncher + openIsInteractive for the duration of a
// test, capturing the URL that would be handed to the platform opener.
func stubLauncher(t *testing.T, interactive bool, launchErr error) *string {
	t.Helper()
	var got string
	prevLaunch, prevInteractive := browserLauncher, openIsInteractive
	browserLauncher = func(url string) error { got = url; return launchErr }
	openIsInteractive = func(*cobra.Command) bool { return interactive }
	t.Cleanup(func() {
		browserLauncher = prevLaunch
		openIsInteractive = prevInteractive
	})
	return &got
}

func TestOpen_bySlug_invokesOpener(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "", "mh1", nil)
	got := stubLauncher(t, true, nil)

	out, _, err := executeIn(t, dir, "open", "s1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if *got != "https://mdfly.dev/s1" {
		t.Errorf("launched url=%q, want https://mdfly.dev/s1", *got)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("interactive open should not print the URL, got %q", out)
	}
}

func TestOpen_byFile_disambiguation(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "notes.md")
	seedRecord(t, dir, "s1", "", "mh1", &abs)
	seedRecord(t, dir, "s2", "", "mh2", &abs)
	got := stubLauncher(t, true, nil)

	_, _, err := executeIn(t, dir, "open", abs)
	if err == nil {
		t.Fatal("ambiguous file should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want usage", output.ExitCode(err))
	}

	if _, _, err := executeIn(t, dir, "open", abs, "--slug", "s2"); err != nil {
		t.Fatalf("open --slug s2: %v", err)
	}
	if *got != "https://mdfly.dev/s2" {
		t.Errorf("launched url=%q, want .../s2", *got)
	}
}

func TestOpen_unresolvable_exit2(t *testing.T) {
	dir := t.TempDir()
	stubLauncher(t, true, nil)

	_, _, err := executeIn(t, dir, "open", "ghost")
	if err == nil {
		t.Fatal("unresolvable target should error")
	}
	if output.ExitCode(err) != output.ExitUsage {
		t.Errorf("exit=%d, want 2 (usage)", output.ExitCode(err))
	}
}

func TestOpen_nonTTY_printsURLToStdout(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "", "mh1", nil)
	stubLauncher(t, false, nil) // non-interactive

	out, _, err := executeIn(t, dir, "open", "s1")
	if err != nil {
		t.Fatalf("open non-TTY: %v", err)
	}
	if strings.TrimSpace(out) != "https://mdfly.dev/s1" {
		t.Errorf("non-TTY open should print URL to stdout, got %q", out)
	}
}

func TestOpen_launchFails_fallsBackToStdout(t *testing.T) {
	dir := t.TempDir()
	seedRecord(t, dir, "s1", "", "mh1", nil)
	stubLauncher(t, true, errLaunch)

	out, _, err := executeIn(t, dir, "open", "s1")
	if err != nil {
		t.Fatalf("open with failing opener should still exit 0: %v", err)
	}
	if strings.TrimSpace(out) != "https://mdfly.dev/s1" {
		t.Errorf("opener-unavailable should fall back to stdout, got %q", out)
	}
}
