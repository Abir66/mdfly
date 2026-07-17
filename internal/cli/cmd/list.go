package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/spf13/cobra"
)

// textPathLabel is the path column for a Text Publish, which has no source file.
const textPathLabel = "text"

// newListCmd wires the list verb: print every tracked record newest-first from
// local state only, with no network call. --json emits a machine-readable array.
func newListCmd(app *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List documents you have published",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runList(cmd)
		},
	}
}

func (app *appContext) runList(cmd *cobra.Command) error {
	ledger, err := localstate.New(app.cfg.StateDir).Load()
	if err != nil {
		return err
	}
	records := ledger.All()
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})

	if app.json {
		return writeListJSON(cmd.OutOrStdout(), records)
	}
	if len(records) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "nothing published yet")
		return nil
	}
	return writeListTable(cmd.OutOrStdout(), records)
}

// writeListJSON emits the records as a JSON array on stdout, always a valid
// array (an empty ledger yields []).
func writeListJSON(stdout io.Writer, records []localstate.Record) error {
	if records == nil {
		records = []localstate.Record{}
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(records)
}

// writeListTable renders a tab-aligned table: slug, tier, updated-at, path (or
// "text"), URL.
func writeListTable(stdout io.Writer, records []localstate.Record) error {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tTIER\tUPDATED\tPATH\tURL")
	for _, rec := range records {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			rec.Slug, rec.Tier, rec.UpdatedAt.Format(time.RFC3339), pathLabel(rec), rec.URL)
	}
	return tw.Flush()
}

// pathLabel is the record's source path, or "text" for a Text Publish.
func pathLabel(rec localstate.Record) string {
	if rec.Path == nil {
		return textPathLabel
	}
	return *rec.Path
}
