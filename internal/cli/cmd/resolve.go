package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/Abir66/mdfly/internal/cli/localstate"
	"github.com/Abir66/mdfly/internal/cli/output"
)

// resolveLocalSlug maps arg/--slug to a tracked slug using only local state,
// with no server-recovery branch (unlike delete). Resolution: --slug wins; else
// a direct slug hit; else the arg is treated as a file path looked up in the
// path→slugs index (1 → that slug; 0 → not-tracked error; many → --slug error).
// The returned slug for --slug is not verified against the ledger; callers that
// need the record must Get it and handle a miss.
func resolveLocalSlug(ledger *localstate.Ledger, arg, slugFlag, verb string) (string, error) {
	if slugFlag != "" {
		return slugFlag, nil
	}
	if arg == "" {
		return "", &output.UsageError{Err: fmt.Errorf("%s requires a slug or file argument (or --slug)", verb)}
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
		return "", &output.UsageError{Err: fmt.Errorf("no tracked document for %q; run \"mdfly list\" to see tracked documents", arg)}
	default:
		return "", &output.UsageError{Err: fmt.Errorf("%q maps to %d documents; pass --slug to choose", arg, len(slugs))}
	}
}
