package publish

import (
	"path/filepath"
	"time"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/cli/input"
	"github.com/Abir66/mdfly/internal/cli/localstate"
)

// persistPublish records the freshly published document in Local State and
// stores its Edit Token in the separate credentials file (ADR-0009/ADR-0008).
// A file publish records the absolute source path; a Text Publish records a nil
// path. It is called only after a successful commit.
func persistPublish(stateDir string, src input.Source, bundle Bundle, commit api.CommitResponse, editToken string) error {
	store := localstate.New(stateDir)
	now := time.Now().UTC()

	source := localstate.SourceText
	var path *string
	if src.Kind == input.KindFile {
		source = localstate.SourceFile
		if abs, err := filepath.Abs(src.Path); err == nil {
			path = &abs
		}
	}

	rec := localstate.Record{
		Slug:         commit.Slug,
		URL:          commit.URL,
		ManifestHash: commit.ManifestHash,
		Tier:         tierAnon,
		Size:         bundle.TotalBytes(),
		FileCount:    len(bundle.FilesByPath),
		CreatedAt:    now,
		UpdatedAt:    now,
		Source:       source,
		Path:         path,
		HasToken:     true,
	}
	// Save the token before the record so a record with HasToken:true never
	// exists without its token; roll the token back if the record fails.
	if err := store.SaveToken(commit.Slug, editToken); err != nil {
		return err
	}
	if err := store.Upsert(rec); err != nil {
		store.DeleteToken(commit.Slug) //nolint:errcheck // best-effort rollback
		return err
	}
	return nil
}
