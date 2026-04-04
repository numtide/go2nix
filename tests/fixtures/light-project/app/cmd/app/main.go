package main

import (
	"fmt"
	"os"

	"github.com/numtide/go2nix/light/internal/config"
	"github.com/numtide/go2nix/light/internal/core"
	"github.com/numtide/go2nix/light/internal/handler"
	"github.com/numtide/go2nix/light/internal/middleware"
	"github.com/numtide/go2nix/light/internal/router"
	"github.com/numtide/go2nix/light/internal/util"
)

func main() {
	if err := core.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "core: %v\n", err)
		os.Exit(1)
	}
	if err := util.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "util: %v\n", err)
		os.Exit(1)
	}
	if err := config.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if err := middleware.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "middleware: %v\n", err)
		os.Exit(1)
	}
	if err := handler.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "handler: %v\n", err)
		os.Exit(1)
	}
	if err := router.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "router: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ok")
}
