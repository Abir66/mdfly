package handlers

import (
	"net/http"
	"strings"

	"github.com/Abir66/mdfly/internal/server/httpx"
	"github.com/Abir66/mdfly/internal/server/service/document"
)

// DeleteDocument handles DELETE /v1/documents/{slug}: Edit-Token-authenticated
// soft delete (ADR-0015). Success is 204 No Content.
func DeleteDocument(svc *document.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if herr := svc.Delete(r.Context(), r.PathValue("slug"), token); herr != nil {
			httpx.WriteError(w, herr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header
// (ADR-0015). Returns "" when the header is absent or not a bearer scheme.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
