package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/publish"
)

// Init handles POST /v1/publish/init.
func Init(svc *publish.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.InitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, httpx.BadRequest("invalid JSON body"))
			return
		}

		res, herr := svc.Init(r.Context(), req)
		if herr != nil {
			httpx.WriteError(w, herr)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, api.InitResponse{
			Slug:          res.Slug,
			PresignedURLs: res.PresignedURLs,
			InlineAccept:  res.InlineAccept,
		})
	}
}

// Commit handles POST /v1/publish/commit.
func Commit(svc *publish.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.CommitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, httpx.BadRequest("invalid JSON body"))
			return
		}

		res, herr := svc.Commit(r.Context(), req)
		if herr != nil {
			httpx.WriteError(w, herr)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, api.CommitResponse{
			URL:          res.URL,
			Slug:         res.Slug,
			ManifestHash: res.ManifestHash,
		})
	}
}
