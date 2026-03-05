// Root module for the tooling in this repo (generator/, go2nix/).
// The torture test subject has its own go.mod at torture/go.mod.
module github.com/numtide/go2nix

go 1.24.0

require (
	github.com/BurntSushi/toml v1.6.0
	golang.org/x/mod v0.32.0
	golang.org/x/sync v0.19.0
)

require github.com/nix-community/go-nix v0.0.0-20250101154619-4bdde671e0a1 // indirect
