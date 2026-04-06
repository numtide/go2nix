package router

import (
	"github.com/numtide/go2nix/light/internal/config"
	"github.com/numtide/go2nix/light/internal/core"
	"github.com/numtide/go2nix/light/internal/handler"
	"github.com/numtide/go2nix/light/internal/middleware"
	"github.com/numtide/go2nix/light/internal/util"
)

// Route dispatches input to handlers.
func Route(cfg *config.Config, path string) string {
	middleware.Logger(cfg, "routing: "+path)
	_ = util.Slugify(path)
	_ = core.Hash(path)
	return handler.Handle(cfg, path)
}

// Run is the entry point for benchmarking.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_ = Route(cfg, "/api/test")
	return nil
}
