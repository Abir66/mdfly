package handlers

import (
	_ "embed"
	"fmt"
	"net/http"
)

const (
	contentTypePlain = "text/plain; charset=utf-8"
	contentTypeShell = "text/x-shellscript; charset=utf-8"

	// installScriptCacheControl keeps the installer at the edge briefly. The
	// mdfly.dev cache rule caches whatever carries a Cache-Control and bypasses
	// what does not, and nothing purges this URL (ADR-0012 covers slugs only),
	// so the TTL is the only way a fixed installer reaches users.
	installScriptCacheControl = "public, max-age=300"

	robotsTxtBody = "User-agent: *\nDisallow: /\n"
	healthzBody   = "ok"
)

//go:embed assets/install.sh
var installScript []byte

// Robots serves /robots.txt: a blanket Disallow keeping every slug out of search
// indexes (CONTEXT.md visibility / ADR-0011). No cache headers — it is tiny and
// static, and a fresh fetch costs nothing.
func Robots() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypePlain)
		fmt.Fprint(w, robotsTxtBody)
	}
}

// InstallScript serves /install.sh, the POSIX-sh installer, from bytes embedded
// at build time. Serving it from the apex rather than raw.githubusercontent.com
// keeps the documented install command on a domain mdfly controls.
func InstallScript() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypeShell)
		w.Header().Set("Cache-Control", installScriptCacheControl)
		w.Write(installScript)
	}
}

// Healthz serves /healthz for the platform liveness probe (ADR-0003). It touches
// no DB or R2 — it reports process liveness only, so it stays green even when
// Postgres is unreachable.
func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypePlain)
		fmt.Fprint(w, healthzBody)
	}
}
