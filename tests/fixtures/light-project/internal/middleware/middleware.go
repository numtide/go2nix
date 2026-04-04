package middleware

import (
	"log"

	"github.com/numtide/go2nix/light/internal/config"
	"github.com/numtide/go2nix/light/internal/core"
	"github.com/numtide/go2nix/light/internal/util"
)

// Logger logs request info.
func Logger(cfg *config.Config, msg string) {
	tag := util.Slugify(cfg.Name)
	hash := core.Hash(msg)
	if cfg.Debug {
		log.Printf("[%s] %s (%s)", tag, msg, hash[:8])
	}
}

// Run is the entry point for benchmarking.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	Logger(cfg, "startup")
	return nil
}
