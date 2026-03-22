package main

import (
	"fmt"
	"os"

	"github.com/numtide/go2nix/torture-generator/cmd"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <command> <outdir>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  torture   Generate torture-test multi-module Go project\n")
		fmt.Fprintf(os.Stderr, "  fixtures  Generate test fixtures\n")
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "torture":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: %s torture <outdir>\n", os.Args[0])
			os.Exit(1)
		}
		if err := cmd.RunTorture(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "fixtures":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: %s fixtures <outdir>\n", os.Args[0])
			os.Exit(1)
		}
		if err := cmd.RunFixtures(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(1)
	}
}
