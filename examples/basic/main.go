// Example basic HTTP server with hot reload support
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jamra/hotswap/takeover"
)

var startTime = time.Now()

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from HOTSWAP v8!\n")
		// fmt.Fprintf(w, "This is the edited version.\n")
		fmt.Fprintf(w, "Server started: %s\n", startTime.Format(time.RFC3339))
		fmt.Fprintf(w, "PID: %d\n", os.Getpid())
		fmt.Fprintf(w, "Uptime: %s\n", time.Since(startTime).Round(time.Second))
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	log.Printf("Starting server on %s (PID: %d)", addr, os.Getpid())

	opts := takeover.Options{
		DrainTimeout: 30 * time.Second,
	}

	if err := takeover.ListenAndServe(addr, mux, opts); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
