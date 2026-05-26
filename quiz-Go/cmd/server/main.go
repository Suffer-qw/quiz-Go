package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"quiz-go/internal/app"
)

func main() {
	addr := envOr("ADDR", ":8000")
	dbPath := envOr("DB_PATH", "./data/app.db")

	a, err := app.New(app.Config{
		DBPath:        dbPath,
		SessionMaxAge: 14 * 24 * time.Hour,
	})
	if err != nil {
		log.Fatalf("init app: %v", err)
	}

	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           a.WithMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on http://localhost%s", addr)
	log.Fatal(srv.ListenAndServe())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

