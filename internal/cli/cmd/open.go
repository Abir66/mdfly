package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
	"github.com/spf13/cobra"
)

// browserLauncher starts the platform browser for a URL, returning any launch
// error. It is a package var so tests can stub the platform opener.
var browserLauncher = launchBrowser

// openIsInteractive reports whether an interactive browser open makes sense —
// stdout is a terminal. A package var so tests can force either path.
var openIsInteractive = func(cmd *cobra.Command) bool {
	f, ok := cmd.OutOrStdout().(*os.File)
	return ok && input.IsTerminal(f)
}

// newOpenCmd wires the open verb: resolve a tracked document's URL from local
// state (by slug or file, same disambiguation as delete/remove) and launch the
// browser. In a non-TTY context, or if the opener is unavailable, the URL is
// printed to stdout instead of failing.
func newOpenCmd(app *appContext) *cobra.Command {
	var slugFlag string

	cmd := &cobra.Command{
		Use:   "open [slug-or-file]",
		Short: "Open a published document in the browser",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runOpen(cmd, firstArg(args), slugFlag)
		},
	}
	cmd.Flags().StringVar(&slugFlag, "slug", "", "target document by slug (disambiguates a file mapped to several)")
	return cmd
}

func (app *appContext) runOpen(cmd *cobra.Command, arg, slugFlag string) error {
	ledger, err := localstate.New(app.cfg.StateDir).Load()
	if err != nil {
		return err
	}

	slug, err := resolveLocalSlug(ledger, arg, slugFlag, "open")
	if err != nil {
		return err
	}
	rec, ok := ledger.Get(slug)
	if !ok {
		return &output.UsageError{Err: fmt.Errorf("no tracked document with slug %q; run \"mdfly list\" to see tracked documents", slug)}
	}

	stdout := cmd.OutOrStdout()
	if !openIsInteractive(cmd) {
		fmt.Fprintln(stdout, rec.URL)
		return nil
	}
	if err := browserLauncher(rec.URL); err != nil {
		fmt.Fprintln(stdout, rec.URL) // opener unavailable: fall back to stdout
	}
	return nil
}

// openURL launches the system browser for url, reporting a warning to w on
// failure. Used by publish --open, where a failed browser is never fatal — the
// URL is still on stdout for the user to copy.
func openURL(w io.Writer, url string) {
	if err := browserLauncher(url); err != nil {
		fmt.Fprintf(w, "could not open browser: %v\n", err)
	}
}

// launchBrowser starts the platform's URL-opening command and reaps the child.
func launchBrowser(url string) error {
	name, args := openCommand(url)
	c := exec.Command(name, args...)
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait() //nolint:errcheck // reap the child so its resources are released
	return nil
}

// openCommand returns the platform's URL-opening command.
func openCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
