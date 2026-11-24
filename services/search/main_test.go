package main

import (
	"net/http"
	"testing"
)

func TestNewHTTPServer_SetsAllTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())

	timeouts := map[string]int64{
		"ReadHeaderTimeout": int64(srv.ReadHeaderTimeout),
		"ReadTimeout":       int64(srv.ReadTimeout),
		"WriteTimeout":      int64(srv.WriteTimeout),
		"IdleTimeout":       int64(srv.IdleTimeout),
	}
	for name, v := range timeouts {
		if v <= 0 {
			t.Errorf("%s is not set", name)
		}
	}
	if srv.WriteTimeout <= srv.ReadTimeout {
		t.Errorf("WriteTimeout (%s) should exceed the 30s handler timeout so a 504 can be written", srv.WriteTimeout)
	}
}
