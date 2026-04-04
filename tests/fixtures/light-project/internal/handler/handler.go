package handler

import (
	"fmt"

	"github.com/numtide/go2nix/light/internal/config"
	"github.com/numtide/go2nix/light/internal/core"
	"github.com/numtide/go2nix/light/internal/middleware"
	"github.com/numtide/go2nix/light/internal/util"
)

// Handle processes a request.
func Handle(cfg *config.Config, input string) string {
	middleware.Logger(cfg, "handling: "+input)
	slug := util.Slugify(input)
	hash := core.Hash(input)
	return fmt.Sprintf("slug=%s hash=%s", slug, hash[:16])
}

// Run is the entry point for benchmarking.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_ = Handle(cfg, "test")
	return nil
}
