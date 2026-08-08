package server

import (
	"net/http"

	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/middleware"
)

// routes builds the server's HTTP handler: route table + global middleware. The
// write endpoints additionally carry the app rate limiter (ADR-0013); read paths
// are edge-cached and never limited.
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/publish/init", a.limited(handlers.Init(a.publish)))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(a.publish))
	mux.Handle("POST /v1/update/init", a.limited(handlers.UpdateInit(a.publish)))
	mux.HandleFunc("POST /v1/update/commit", handlers.UpdateCommit(a.publish))
	mux.Handle("DELETE /v1/documents/{slug}", a.limited(handlers.DeleteDocument(a.document)))
	mux.HandleFunc("GET /healthz", handlers.Healthz())
	mux.HandleFunc("GET /robots.txt", handlers.Robots())
	mux.HandleFunc("GET /install.sh", handlers.InstallScript())
	mux.Handle("GET /_static/", a.static.Handler())
	mux.HandleFunc("GET /raw/{slug}", handlers.Raw(a.raw))
	mux.HandleFunc("GET /raw/{slug}/{path...}", handlers.RawPath(a.raw))
	mux.HandleFunc("GET /{slug}", handlers.View(a.view))
	mux.HandleFunc("GET /{slug}/{path...}", handlers.ViewPath(a.view))
	return middleware.Logger(middleware.Recover(mux))
}

// limited wraps h in the rate-limit middleware, or returns it untouched when the
// limiter is disabled (REDIS_URL absent — local dev and tests).
func (a *App) limited(h http.HandlerFunc) http.Handler {
	if a.limiter == nil {
		return h
	}
	return middleware.RateLimit(a.limiter)(h)
}
