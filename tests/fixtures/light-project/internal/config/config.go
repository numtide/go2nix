package config

import (
	"os"

	"github.com/numtide/go2nix/light/internal/core"
	"github.com/numtide/go2nix/light/internal/util"
)

// Config holds application configuration.
type Config struct {
	Name  string
	Slug  string
	Hash  string
	Debug bool
}

// Load returns a Config from environment.
func Load() (*Config, error) {
	name := os.Getenv("APP_NAME")
	if name == "" {
		name = "light-app"
	}
	return &Config{
		Name:  name,
		Slug:  util.Slugify(name),
		Hash:  core.Hash(name),
		Debug: os.Getenv("DEBUG") == "1",
	}, nil
}

// Run is the entry point for benchmarking.
func Run() error {
	_, err := Load()
	return err
}
