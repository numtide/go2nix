package util

import (
	"strings"

	"github.com/numtide/go2nix/light/internal/core"
)

// Slugify converts a string to a URL-friendly slug using core.Hash for uniqueness.
func Slugify(s string) string {
	slug := strings.ToLower(strings.ReplaceAll(s, " ", "-"))
	return slug + "-" + core.Hash(slug)[:8]
}

// Run is the entry point for benchmarking.
func Run() error {
	_ = Slugify("hello world")
	return nil
}
