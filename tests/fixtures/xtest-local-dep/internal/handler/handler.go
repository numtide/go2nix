package handler

import "example.com/xtest-local-dep/internal/server"

// Handle processes a request using the server.
func Handle(req string) string {
	return server.Start() + ": " + req
}
