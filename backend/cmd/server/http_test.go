package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPServerRejectsSlowHeaders(t *testing.T) {
	server, err := publicHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("incomplete headers reached handler") }), func(key string) string {
		if key == "HTTP_READ_HEADER_TIMEOUT" {
			return "30ms"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-done }()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(conn, "GET /healthz HTTP/1.1\r\nHost: localhost\r\nX-Slow: ")
	if err != nil {
		t.Fatal(err)
	}
	// The server must close on its header deadline, without a completed request.
	buf := make([]byte, 1024)
	_, err = conn.Read(buf)
	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
		t.Fatal("server failed to enforce header deadline")
	}
	if err == nil && !strings.Contains(string(buf), "400") {
		t.Fatalf("unexpected slow-header response: %q", buf)
	}
}

func TestHTTPServerTimeoutConfig(t *testing.T) {
	defaults, err := publicHTTPServer("", nil, func(string) string { return "" })
	if err != nil || defaults.ReadHeaderTimeout <= 0 || defaults.ReadTimeout <= 0 || defaults.WriteTimeout <= 0 || defaults.IdleTimeout <= 0 || defaults.MaxHeaderBytes <= 0 {
		t.Fatalf("unsafe defaults: %+v %v", defaults, err)
	}
	for _, key := range []string{"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "HTTP_MAX_HEADER_BYTES"} {
		for _, invalid := range []string{"0", "-1", "oops"} {
			_, err := publicHTTPServer("", nil, func(name string) string {
				if name == key {
					return invalid
				}
				return ""
			})
			if err == nil {
				t.Fatalf("accepted %s=%s", key, invalid)
			}
		}
	}
}
