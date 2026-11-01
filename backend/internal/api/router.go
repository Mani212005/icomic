package api

import (
	"net/http"
)

func (s *Server) NewRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.HealthCheck)
	mux.HandleFunc("GET /internal/health", s.HealthCheck)
	return mux
}
