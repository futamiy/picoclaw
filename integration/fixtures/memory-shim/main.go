// Command memory-shim is an integration-test fixture that exposes the
// pkg/memtest fake memory-provider server over a real HTTP listener. It lets
// the Docker integration suite exercise the memory pipelines across container
// boundaries: the runner pushes transcripts to this shim exactly as it would to
// a real Honcho/Mem0/RetainDB server, and reads back what the shim recorded.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/sipeed/picoclaw/pkg/memtest"
)

func main() {
	fs := memtest.NewServer()

	mux := http.NewServeMux()

	// Introspection endpoints used by the test runner to inspect/reset state.
	// Namespaced under __ to avoid colliding with provider paths.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/__recorded", func(w http.ResponseWriter, r *http.Request) {
		type recordedView struct {
			Provider string `json:"provider"`
			Method   string `json:"method"`
			Path     string `json:"path"`
			Query    string `json:"query"`
			Body     string `json:"body"`
			Auth     string `json:"auth"`
		}
		reqs := fs.Requests()
		views := make([]recordedView, 0, len(reqs))
		for _, req := range reqs {
			views = append(views, recordedView{
				Provider: string(req.Provider),
				Method:   req.Method,
				Path:     req.Path,
				Query:    req.Query,
				Body:     string(req.Body),
				Auth:     req.Auth,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(views)
	})
	mux.HandleFunc("/__reset", func(w http.ResponseWriter, r *http.Request) {
		fs.Reset()
		w.WriteHeader(http.StatusOK)
	})

	// Everything else is served by the fake provider implementation.
	mux.Handle("/", fs)

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("memory-shim listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
