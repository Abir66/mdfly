package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Abir66/mdfly/internal/cli/config"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/output"
	"github.com/Abir66/mdfly/internal/cli/publish"
	"github.com/spf13/cobra"
)

// errNotImplemented is returned by verb stubs that have no behavior yet.
var errNotImplemented = errors.New("not implemented")

// errNoCommand is returned when mdfly is invoked without a valid subcommand.
var errNoCommand = errors.New("a subcommand is required")

// appContext holds the global-flag values and resolved config, populated once
// in PersistentPreRunE and shared by every verb.
type appContext struct {
	json          bool
	verbose       bool
	quiet         bool
	yes           bool
	noUpdateCheck bool
	apiFlag       string

	cfg config.Config
}

// NewRootCmd builds the mdfly command tree. version is printed by --version.
func NewRootCmd(version string) *cobra.Command {
	app := &appContext{}

	root := &cobra.Command{
		Use:           "mdfly",
		Short:         "Publish and share markdown from the command line",
		Version:       version,
		SilenceErrors: true,
		// Usage stays enabled through flag parsing so flag-parse errors show
		// it; PersistentPreRunE silences it before any RunE runs, so runtime
		// errors do not dump usage.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cfg, err := config.Resolve(app.apiFlag)
			if err != nil {
				return err
			}
			app.cfg = cfg
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.ErrOrStderr(), cmd.UsageString())
			return &output.UsageError{Err: errNoCommand}
		},
	}
	// Flag-parse failures are input errors (exit 2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &output.UsageError{Err: err}
	})

	registerGlobalFlags(root, app)
	root.AddCommand(
		newPublishCmd(app),
		newStubCmd("update", "Update an already-published document"),
		newListCmd(app),
		newDeleteCmd(app),
		newRemoveCmd(app),
		newOpenCmd(app),
	)
	return root
}

func registerGlobalFlags(root *cobra.Command, app *appContext) {
	pf := root.PersistentFlags()
	pf.BoolVar(&app.json, "json", false, "emit machine-readable JSON output")
	pf.BoolVarP(&app.verbose, "verbose", "v", false, "verbose diagnostic output")
	pf.BoolVarP(&app.quiet, "quiet", "q", false, "suppress non-error output")
	pf.BoolVarP(&app.yes, "yes", "y", false, "assume yes to confirmation prompts")
	pf.StringVar(&app.apiFlag, "api", "", "API base URL override")
	pf.BoolVar(&app.noUpdateCheck, "no-update-check", false, "skip the background update check")
}

// newPublishCmd wires the publish verb: resolve the content source, run the
// publish workflow, print the URL (or --json object), and optionally --open it.
func newPublishCmd(app *appContext) *cobra.Command {
	var recursive, open bool
	var message string

	cmd := &cobra.Command{
		Use:   "publish [file]",
		Short: "Publish a file or inline text to a shareable URL",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := input.Resolve(input.Request{
				File:       firstArg(args),
				Message:    message,
				MessageSet: cmd.Flags().Changed("message"),
				Stdin:      cmd.InOrStdin(),
				StdinIsTTY: input.IsTerminal(os.Stdin),
			})
			if err != nil {
				return err
			}

			res, err := publish.Run(publish.Options{
				APIBase:   app.cfg.APIBase,
				StateDir:  app.cfg.StateDir,
				Source:    src,
				Recursive: recursive,
				Progress:  app.progressWriter(cmd),
			})
			if err != nil {
				return err
			}

			if err := writePublishResult(cmd.OutOrStdout(), res, app.json); err != nil {
				return err
			}
			if open {
				openURL(cmd.ErrOrStderr(), res.URL)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&recursive, "recursive", "r", false, "follow linked .md files transitively")
	f.StringVarP(&message, "message", "m", "", "publish inline text instead of a file")
	f.BoolVar(&open, "open", false, "open the resulting URL in the browser after publishing")
	return cmd
}

// writePublishResult writes the canonical artifact to stdout: the URL on one
// line, or the machine-readable object under --json.
func writePublishResult(stdout io.Writer, res publish.Result, asJSON bool) error {
	if asJSON {
		obj := struct {
			URL          string `json:"url"`
			Slug         string `json:"slug"`
			ManifestHash string `json:"manifest_hash"`
			Tier         string `json:"tier"`
		}{res.URL, res.Slug, res.ManifestHash, res.Tier}
		return json.NewEncoder(stdout).Encode(obj)
	}
	_, err := fmt.Fprintln(stdout, res.URL)
	return err
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// progressWriter returns the writer for the "uploaded N/M" counter, or nil to
// suppress it: only when not quiet and stderr is an interactive terminal.
func (app *appContext) progressWriter(cmd *cobra.Command) io.Writer {
	if app.quiet {
		return nil
	}
	if f, ok := cmd.ErrOrStderr().(*os.File); ok && input.IsTerminal(f) {
		return cmd.ErrOrStderr()
	}
	return nil
}

func newStubCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented
		},
	}
}
