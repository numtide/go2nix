module github.com/numtide/go2nix/light/internal/config

go 1.25.0

require (
	github.com/numtide/go2nix/light/internal/core v0.0.0
	github.com/numtide/go2nix/light/internal/util v0.0.0
)

replace (
	github.com/numtide/go2nix/light/internal/core => ../core
	github.com/numtide/go2nix/light/internal/util => ../util
)
