package main

import (
	"log"
	"net/http"
	"os"

	"icomic/backend/internal/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := api.NewServer()
	handler := server.NewRouter()

	log.Printf("Starting Icomic Go API server on port %s...", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
