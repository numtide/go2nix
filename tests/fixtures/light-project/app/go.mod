module github.com/numtide/go2nix/light/app

go 1.25.0

require (
	github.com/numtide/go2nix/light/internal/config v0.0.0
	github.com/numtide/go2nix/light/internal/core v0.0.0
	github.com/numtide/go2nix/light/internal/handler v0.0.0
	github.com/numtide/go2nix/light/internal/middleware v0.0.0
	github.com/numtide/go2nix/light/internal/router v0.0.0
	github.com/numtide/go2nix/light/internal/util v0.0.0
)

replace (
	github.com/numtide/go2nix/light/internal/config => ../internal/config
	github.com/numtide/go2nix/light/internal/core => ../internal/core
	github.com/numtide/go2nix/light/internal/handler => ../internal/handler
	github.com/numtide/go2nix/light/internal/middleware => ../internal/middleware
	github.com/numtide/go2nix/light/internal/router => ../internal/router
	github.com/numtide/go2nix/light/internal/util => ../internal/util
)
