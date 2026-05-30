package handlers

import (
	"fmt"
	"net/http"
)

const (
	contentTypePlain = "text/plain; charset=utf-8"

	robotsTxtBody = "User-agent: *\nDisallow: /\n"
	healthzBody   = "ok"
)

// Robots serves /robots.txt: a blanket Disallow keeping every slug out of search
// indexes (CONTEXT.md visibility / ADR-0021). No cache headers — it is tiny and
// static, and a fresh fetch costs nothing.
func Robots() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypePlain)
		fmt.Fprint(w, robotsTxtBody)
	}
}

// Healthz serves /healthz for the platform liveness probe (ADR-0007). It touches
// no DB or R2 — it reports process liveness only, so it stays green even when
// Postgres is unreachable.
func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypePlain)
		fmt.Fprint(w, healthzBody)
	}
}
