package main

import (
	"io"
	"log"
	"net/http"
	"os"
)

const port = "8080"
const logFilePath = "/shared/server.log"

func main() {

	// Create HTTP handler that serves log file contents
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(logFilePath)
		if err != nil {
			http.Error(w, "Log file not found", http.StatusNotFound)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/plain")
		io.Copy(w, f)
	})

	// Start the server
	addr := ":" + port
	log.Printf("Starting server on port %s", port)
	log.Printf("Serving contents of %s", logFilePath)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
