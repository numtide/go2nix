package integration_test

import (
	"testing"

	"example.com/xtest-local-dep/internal/handler"
	"example.com/xtest-local-dep/internal/server"
)

func TestIntegration(t *testing.T) {
	if server.Start() != "running" {
		t.Fatal("server.Start() != \"running\"")
	}
	if handler.Handle("ping") != "running: ping" {
		t.Fatalf("handler.Handle(\"ping\") = %q, want \"running: ping\"", handler.Handle("ping"))
	}
}
