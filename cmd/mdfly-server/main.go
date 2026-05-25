package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"

	"github.com/Abir66/mdfly/internal/server/handlers"
	pgstore "github.com/Abir66/mdfly/internal/server/store/postgres"
	r2store "github.com/Abir66/mdfly/internal/server/store/r2"
)

func main() {
	dsn := envOrDefault("DATABASE_URL", "postgres://mdfly:secret@localhost:5432/mdfly?sslmode=disable")
	baseURL := envOrDefault("BASE_URL", "http://localhost:8080")

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	r2 := r2store.New(r2store.Config{
		Endpoint:        envOrDefault("R2_ENDPOINT", "http://localhost:9000"),
		AccessKeyID:     envOrDefault("R2_ACCESS_KEY_ID", "minioadmin"),
		SecretAccessKey: envOrDefault("R2_SECRET_ACCESS_KEY", "minioadmin"),
		Bucket:          envOrDefault("R2_BUCKET", "mdfly"),
		PublicBaseURL:   envOrDefault("CDN_BASE_URL", "http://localhost:9000/mdfly"),
	})

	deps := handlers.PublishDeps{
		PG:      pgstore.New(db),
		R2:      r2,
		BaseURL: baseURL,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish/init", handlers.Init(deps))
	mux.HandleFunc("POST /v1/publish/commit", handlers.Commit(deps))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	addr := envOrDefault("ADDR", ":8080")
	log.Printf("mdfly-server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
