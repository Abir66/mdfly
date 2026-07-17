package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
	"github.com/Abir66/mdfly/internal/cli/publish"
	"github.com/spf13/cobra"
)

const deleteHTTPTimeout = 30 * time.Second

// newDeleteCmd wires the delete verb: resolve the slug from local state (by
// slug or file, same disambiguation as update), confirm, DELETE server-side,
// then prune the local record. A server 404/410 also prunes (self-heal).
func newDeleteCmd(app *appContext) *cobra.Command {
	var slugFlag string

	cmd := &cobra.Command{
		Use:   "delete [slug-or-file]",
		Short: "Delete a published document from the server",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runDelete(cmd, firstArg(args), slugFlag)
		},
	}
	cmd.Flags().StringVar(&slugFlag, "slug", "", "target document by slug (disambiguates a file mapped to several)")
	return cmd
}

func (app *appContext) runDelete(cmd *cobra.Command, arg, slugFlag string) error {
	store := localstate.New(app.cfg.StateDir)
	ledger, err := store.Load()
	if err != nil {
		return err
	}

	slug, err := resolveDeleteTarget(ledger, arg, slugFlag)
	if err != nil {
		return err
	}

	ok, err := app.confirmDelete(cmd, slug)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
		return nil
	}

	token, _, err := store.LoadToken(slug)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: deleteHTTPTimeout}
	err = publish.DeleteDocument(context.Background(), client, publish.DeleteOptions{
		APIBase: app.cfg.APIBase,
		Slug:    slug,
		Token:   token,
	})
	if err != nil && !isAlreadyGone(err) {
		return err
	}
	if isAlreadyGone(err) {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s was already gone on the server; pruning local record\n", slug)
	}

	pruneLocal(store, slug)
	return writeDeleteResult(cmd.OutOrStdout(), slug, app.json, app.quiet)
}

// resolveDeleteTarget maps arg/--slug to a target slug. Resolution: --slug
// wins; else a direct slug hit; else the arg is treated as a file path and
// looked up in the path→slugs index (0 → error if the file exists, else
// recovery-by-slug; 1 → that slug; many → ambiguous, requires --slug).
func resolveDeleteTarget(ledger *localstate.Ledger, arg, slugFlag string) (string, error) {
	if slugFlag != "" {
		return slugFlag, nil
	}
	if arg == "" {
		return "", &output.UsageError{Err: errors.New("delete requires a slug or file argument (or --slug)")}
	}
	if _, ok := ledger.Get(arg); ok {
		return arg, nil
	}

	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	slugs := ledger.SlugsForPath(abs)
	switch len(slugs) {
	case 1:
		return slugs[0], nil
	case 0:
		if fileExists(arg) {
			return "", &output.UsageError{Err: fmt.Errorf("no published document tracked for %q", arg)}
		}
		return arg, nil // recovery: attempt a server delete by slug
	default:
		return "", &output.UsageError{Err: fmt.Errorf("%q maps to %d documents; pass --slug to choose", arg, len(slugs))}
	}
}

// confirmDelete returns whether the delete should proceed. -y/--yes skips the
// prompt; a non-interactive stdin without -y is a usage error (can't prompt).
func (app *appContext) confirmDelete(cmd *cobra.Command, slug string) (bool, error) {
	if app.yes {
		return true, nil
	}
	in := cmd.InOrStdin()
	if !readerIsTerminal(in) {
		return false, &output.UsageError{Err: fmt.Errorf("refusing to delete %q without confirmation; re-run with -y", slug)}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Delete %s? [y/N] ", slug)
	return readYes(in), nil
}

// readYes reports whether the next line on r is an affirmative (y/yes).
func readYes(r io.Reader) bool {
	line, _ := bufio.NewReader(r).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// readerIsTerminal reports whether r is an interactive terminal — an *os.File
// backed by a TTY. A piped/redirected stdin or a test buffer is not.
func readerIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && input.IsTerminal(f)
}

// pruneLocal removes the record and its Edit Token; failures are recoverable
// anomalies (the document is already gone server-side), warned not fatal.
func pruneLocal(store *localstate.Store, slug string) {
	if err := store.Prune(slug); err != nil {
		slog.Warn("prune local record failed", "slug", slug, "err", err)
	}
	if err := store.DeleteToken(slug); err != nil {
		slog.Warn("delete edit token failed", "slug", slug, "err", err)
	}
}

func writeDeleteResult(stdout io.Writer, slug string, asJSON, quiet bool) error {
	if asJSON {
		return json.NewEncoder(stdout).Encode(struct {
			Slug string `json:"slug"`
		}{slug})
	}
	if quiet {
		return nil
	}
	_, err := fmt.Fprintf(stdout, "Deleted: %s\n", slug)
	return err
}

// isAlreadyGone reports whether err is a server 404/410 — the slug is already
// gone, so the delete self-heals the local record instead of failing.
func isAlreadyGone(err error) bool {
	var apiErr *publish.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusGone)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
