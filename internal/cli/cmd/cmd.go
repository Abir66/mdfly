package cmd

import (
	"errors"
	"fmt"

	"github.com/Abir66/mdfly/internal/cli/config"
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
		newStubCmd("list", "List documents you have published"),
		newStubCmd("delete", "Delete a published document from the server"),
		newStubCmd("remove", "Remove a document from local state only"),
		newStubCmd("open", "Open a published document in the browser"),
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

// newPublishCmd wires the only verb with real behavior in this slice. The
// orchestrator is rewired in S32; here it keeps the existing single-file path.
func newPublishCmd(app *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "publish <file>",
		Short: "Publish markdown to a shareable URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url, err := publish.Run(app.cfg.APIBase, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), url)
			return nil
		},
	}
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
