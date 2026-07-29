package handlers

import (
	"net/http"

	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/document"
)

// DeleteDocument handles DELETE /v1/documents/{slug}: Edit-Token-authenticated
// soft delete (ADR-0008). Success is 204 No Content.
func DeleteDocument(svc *document.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := httpx.BearerToken(r.Header.Get("Authorization"))
		if herr := svc.Delete(r.Context(), r.PathValue("slug"), token); herr != nil {
			httpx.WriteError(w, herr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
