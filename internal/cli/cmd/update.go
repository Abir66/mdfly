package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
	"github.com/Abir66/mdfly/internal/cli/publish"
	"github.com/spf13/cobra"
)

// updateFlags bundles the update verb's flag values.
type updateFlags struct {
	slug       string
	message    string
	messageSet bool
	recursive  bool
	force      bool
	open       bool
}

// newUpdateCmd wires the update verb: resolve the target slug (from --slug or
// the file's path→slugs index), overwrite the document in place at the same URL,
// then re-persist the local record. A server 404/410 prunes the stale record.
func newUpdateCmd(app *appContext) *cobra.Command {
	f := updateFlags{}

	cmd := &cobra.Command{
		Use:   "update [file]",
		Short: "Update an already-published document in place",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.messageSet = cmd.Flags().Changed("message")
			return app.runUpdate(cmd, firstArg(args), f)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&f.slug, "slug", "", "target document by slug (for text updates, recovery, or disambiguation)")
	fl.StringVarP(&f.message, "message", "m", "", "update with inline text instead of a file")
	fl.BoolVarP(&f.recursive, "recursive", "r", false, "follow linked .md files transitively")
	fl.BoolVar(&f.force, "force", false, "overwrite without the optimistic-concurrency check")
	fl.BoolVar(&f.open, "open", false, "open the resulting URL in the browser after updating")
	return cmd
}

func (app *appContext) runUpdate(cmd *cobra.Command, fileArg string, f updateFlags) error {
	store := localstate.New(app.cfg.StateDir)
	ledger, err := store.Load()
	if err != nil {
		return err
	}

	slug, err := app.resolveUpdateTarget(cmd, ledger, fileArg, f.slug)
	if err != nil {
		return err
	}

	src, err := input.Resolve(input.Request{
		File:       fileArg,
		Message:    f.message,
		MessageSet: f.messageSet,
		Stdin:      cmd.InOrStdin(),
		StdinIsTTY: input.IsTerminal(os.Stdin),
	})
	if err != nil {
		return err
	}

	token, _, err := store.LoadToken(slug)
	if err != nil {
		return err
	}

	res, err := publish.RunUpdate(publish.UpdateOptions{
		APIBase:            app.cfg.APIBase,
		StateDir:           app.cfg.StateDir,
		Slug:               slug,
		Token:              token,
		ParentManifestHash: parentHash(ledger, slug, f.force),
		Source:             src,
		Recursive:          f.recursive,
		Progress:           app.progressWriter(cmd),
	})
	if err != nil {
		return app.handleUpdateError(cmd, store, slug, err)
	}

	if err := writePublishResult(cmd.OutOrStdout(), res, app.json); err != nil {
		return err
	}
	if f.open {
		openURL(cmd.ErrOrStderr(), res.URL)
	}
	return nil
}

// parentHash returns the optimistic-concurrency parent to send: the record's
// last-known manifest hash, or "" under --force or when no local record exists
// (recovery), which the server treats as an unguarded overwrite.
func parentHash(ledger *localstate.Ledger, slug string, force bool) string {
	if force {
		return ""
	}
	if rec, ok := ledger.Get(slug); ok {
		return rec.ManifestHash
	}
	return ""
}

// handleUpdateError self-heals a 404/410 by pruning the stale record (the
// document is gone server-side); other errors (409 conflict, auth) propagate to
// the output taxonomy unchanged.
func (app *appContext) handleUpdateError(cmd *cobra.Command, store *localstate.Store, slug string, err error) error {
	if isAlreadyGone(err) {
		pruneLocal(store, slug)
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s is gone on the server; pruning local record\n", slug)
	}
	return err
}

// resolveUpdateTarget maps the file arg and --slug to a target slug. --slug wins
// (targeting/recovery/text updates). Otherwise the file arg is looked up in the
// path→slugs index: 1 → that slug; 0 → a "publish first" hint; many → an
// interactive picker on a TTY, else a --slug-required error.
func (app *appContext) resolveUpdateTarget(cmd *cobra.Command, ledger *localstate.Ledger, fileArg, slugFlag string) (string, error) {
	if slugFlag != "" {
		return slugFlag, nil
	}
	if fileArg == "" {
		return "", &output.UsageError{Err: errors.New("update needs a file argument, or --slug for a text/recovery update")}
	}

	abs, err := filepath.Abs(fileArg)
	if err != nil {
		return "", err
	}
	slugs := ledger.SlugsForPath(abs)
	switch len(slugs) {
	case 1:
		return slugs[0], nil
	case 0:
		return "", &output.UsageError{Err: fmt.Errorf("no published document tracked for %q; run \"mdfly publish %s\" first", fileArg, fileArg)}
	default:
		return app.pickSlug(cmd, ledger, slugs)
	}
}

// pickSlug resolves an ambiguous file→slugs mapping. On an interactive terminal
// it prints a numbered slug+URL menu and reads a choice; otherwise (CI) it is a
// usage error demanding --slug.
func (app *appContext) pickSlug(cmd *cobra.Command, ledger *localstate.Ledger, slugs []string) (string, error) {
	in := cmd.InOrStdin()
	if !readerIsTerminal(in) {
		return "", &output.UsageError{Err: fmt.Errorf("file maps to %d documents; pass --slug to choose", len(slugs))}
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "This file maps to %d documents:\n", len(slugs))
	for i, s := range slugs {
		url := ""
		if rec, ok := ledger.Get(s); ok {
			url = rec.URL
		}
		fmt.Fprintf(out, "  [%d] %s\t%s\n", i+1, s, url)
	}
	fmt.Fprintf(out, "Choose [1-%d]: ", len(slugs))

	line, _ := bufio.NewReader(in).ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(slugs) {
		return "", &output.UsageError{Err: fmt.Errorf("invalid selection %q", strings.TrimSpace(line))}
	}
	return slugs[n-1], nil
}

// isUpdateConflict reports whether err is a 409 optimistic-concurrency conflict.
func isUpdateConflict(err error) bool {
	var apiErr *publish.APIError
	return errors.As(err, &apiErr) &&
		apiErr.Status == http.StatusConflict && apiErr.Code == api.CodeUpdateConflict
}
