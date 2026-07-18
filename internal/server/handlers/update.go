package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/publish"
)

// UpdateInit handles POST /v1/update/init: Edit-Token-authenticated presign of
// only the blobs that changed since the stored manifest (ADR-0027).
func UpdateInit(svc *publish.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.UpdateInitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, httpx.BadRequest("invalid JSON body"))
			return
		}

		res, herr := svc.UpdateInit(r.Context(), req, bearerToken(r.Header.Get("Authorization")))
		if herr != nil {
			httpx.WriteError(w, herr)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, api.UpdateInitResponse{
			PresignedURLs: res.PresignedURLs,
		})
	}
}

// UpdateCommit handles POST /v1/update/commit: atomic optimistic overwrite of
// the live row, slug and URL unchanged.
func UpdateCommit(svc *publish.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.UpdateCommitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, httpx.BadRequest("invalid JSON body"))
			return
		}

		res, herr := svc.UpdateCommit(r.Context(), req, bearerToken(r.Header.Get("Authorization")))
		if herr != nil {
			httpx.WriteError(w, herr)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, api.UpdateCommitResponse{
			URL:          res.URL,
			Slug:         res.Slug,
			ManifestHash: res.ManifestHash,
		})
	}
}
