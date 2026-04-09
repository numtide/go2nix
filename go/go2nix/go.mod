module github.com/numtide/go2nix

go 1.26

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/nix-community/go-nix v0.0.0-20260401165014-ac2535ed3bd6
	golang.org/x/mod v0.32.0
	golang.org/x/sync v0.20.0
)

replace github.com/nix-community/go-nix => github.com/numtide/go-nix v0.0.0-20260409092930-880e947598ce
