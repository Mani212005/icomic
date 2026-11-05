package main

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func publicHTTPServer(addr string, handler http.Handler, getenv func(string) string) (*http.Server, error) {
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 20 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", &server.ReadHeaderTimeout}, {"HTTP_READ_TIMEOUT", &server.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", &server.WriteTimeout}, {"HTTP_IDLE_TIMEOUT", &server.IdleTimeout},
	} {
		if raw := getenv(item.name); raw != "" {
			duration, err := time.ParseDuration(raw)
			if err != nil || duration <= 0 {
				return nil, fmt.Errorf("%s must be a positive duration", item.name)
			}
			*item.target = duration
		}
	}
	if raw := getenv("HTTP_MAX_HEADER_BYTES"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size <= 0 {
			return nil, fmt.Errorf("HTTP_MAX_HEADER_BYTES must be a positive integer")
		}
		server.MaxHeaderBytes = size
	}
	return server, nil
}
