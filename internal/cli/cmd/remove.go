package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
	"github.com/spf13/cobra"
)

// newRemoveCmd wires the remove verb: drop a local tracking entry (record +
// credentials) without touching the server, so the live URL stays up. --all
// wipes the entire ledger behind a TTY confirmation (skipped by -y).
func newRemoveCmd(app *appContext) *cobra.Command {
	var slugFlag string
	var all bool

	cmd := &cobra.Command{
		Use:   "remove [slug-or-file]",
		Short: "Remove a document from local state only",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) > 0 || slugFlag != "" {
					return &output.UsageError{Err: fmt.Errorf("--all cannot be combined with a target or --slug")}
				}
				return app.runRemoveAll(cmd)
			}
			return app.runRemove(cmd, firstArg(args), slugFlag)
		},
	}
	cmd.Flags().StringVar(&slugFlag, "slug", "", "target document by slug (disambiguates a file mapped to several)")
	cmd.Flags().BoolVar(&all, "all", false, "remove every tracked document from local state")
	return cmd
}

func (app *appContext) runRemove(cmd *cobra.Command, arg, slugFlag string) error {
	store := localstate.New(app.cfg.StateDir)
	ledger, err := store.Load()
	if err != nil {
		return err
	}

	slug, err := resolveLocalSlug(ledger, arg, slugFlag, "remove")
	if err != nil {
		return err
	}
	if _, ok := ledger.Get(slug); !ok {
		return &output.UsageError{Err: fmt.Errorf("no tracked document with slug %q", slug)}
	}

	pruneLocal(store, slug)
	return writeRemoveResult(cmd.OutOrStdout(), slug, app.json, app.quiet)
}

func (app *appContext) runRemoveAll(cmd *cobra.Command) error {
	store := localstate.New(app.cfg.StateDir)
	ledger, err := store.Load()
	if err != nil {
		return err
	}
	records := ledger.All()

	ok, err := app.confirmRemoveAll(cmd, len(records))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
		return nil
	}

	for _, rec := range records {
		pruneLocal(store, rec.Slug)
	}
	return writeRemoveAllResult(cmd.OutOrStdout(), len(records), app.json, app.quiet)
}

// confirmRemoveAll gates the ledger wipe: -y skips the prompt; a non-interactive
// stdin without -y is a usage error (can't prompt).
func (app *appContext) confirmRemoveAll(cmd *cobra.Command, n int) (bool, error) {
	if app.yes {
		return true, nil
	}
	in := cmd.InOrStdin()
	if !readerIsTerminal(in) {
		return false, &output.UsageError{Err: fmt.Errorf("refusing to wipe %d local records without confirmation; re-run with -y", n)}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Remove all %d tracked documents from local state? [y/N] ", n)
	return readYes(in), nil
}

func writeRemoveResult(stdout io.Writer, slug string, asJSON, quiet bool) error {
	if asJSON {
		return json.NewEncoder(stdout).Encode(struct {
			Slug string `json:"slug"`
		}{slug})
	}
	if quiet {
		return nil
	}
	_, err := fmt.Fprintf(stdout, "Removed: %s\n", slug)
	return err
}

func writeRemoveAllResult(stdout io.Writer, n int, asJSON, quiet bool) error {
	if asJSON {
		return json.NewEncoder(stdout).Encode(struct {
			Removed int `json:"removed"`
		}{n})
	}
	if quiet {
		return nil
	}
	_, err := fmt.Fprintf(stdout, "Removed %d local record(s)\n", n)
	return err
}
