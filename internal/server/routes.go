package server

import (
	"net/http"

	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/middleware"
)

// routes builds the server's HTTP handler: route table + global middleware.
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(a.publish))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(a.publish))
	mux.HandleFunc("GET /healthz", handlers.Healthz())
	mux.HandleFunc("GET /robots.txt", handlers.Robots())
	mux.HandleFunc("GET /{slug}", handlers.View(a.view))
	mux.HandleFunc("GET /{slug}/{path...}", handlers.ViewPath(a.view))
	return middleware.Logger(middleware.Recover(mux))
}
