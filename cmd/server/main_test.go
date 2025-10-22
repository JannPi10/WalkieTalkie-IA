// cmd/server/main_test.go
package main

import (
	"net/http"
	"testing"
)

func TestBuildServer_DefaultPort(t *testing.T) {
	var dbCalled, routesCalled bool

	addr, handler := buildServer(
		func(string) string { return "" },
		func() { dbCalled = true },
		func(mux *http.ServeMux) {
			if mux == nil {
				t.Fatal("expected mux")
			}
			routesCalled = true
		},
	)

	if !dbCalled {
		t.Error("expected connectDB to be called")
	}
	if !routesCalled {
		t.Error("expected registerRoutes to be called")
	}
	if addr != ":8080" {
		t.Fatalf("expected :8080, got %s", addr)
	}
	if handler == nil {
		t.Fatal("expected handler")
	}
}

func TestBuildServer_CustomPort(t *testing.T) {
	addr, handler := buildServer(
		func(key string) string {
			if key != "PORT" {
				t.Fatalf("unexpected key %s", key)
			}
			return "9090"
		},
		func() {},
		func(*http.ServeMux) {},
	)

	if addr != ":9090" {
		t.Fatalf("expected :9090, got %s", addr)
	}
	if handler == nil {
		t.Fatal("expected handler")
	}
}
