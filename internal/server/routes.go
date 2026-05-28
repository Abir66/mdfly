package server

import (
	"fmt"
	"net/http"

	"github.com/Abir66/mdfly/internal/server/handlers"
	"github.com/Abir66/mdfly/internal/server/middleware"
)

// routes builds the server's HTTP handler: route table + global middleware.
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(a.publish))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(a.publish))
	mux.HandleFunc("GET /{slug}", handlers.View(handlers.ViewDeps{PG: a.db, R2: a.storage}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return middleware.Logger(middleware.Recover(mux))
}
