package main

import (
	"flag"
	"log/slog"
	"os"
	"strings"

	"github.com/numtide/go2nix/pkg/testmain"
)

func runGenTestMainCmd(args []string) {
	fs := flag.NewFlagSet("generate-test-main", flag.ExitOnError)
	importPath := fs.String("import-path", "", "import path of the package under test")
	testFiles := fs.String("test-files", "", "comma-separated absolute paths to internal _test.go files")
	xtestFiles := fs.String("xtest-files", "", "comma-separated absolute paths to external _test.go files")
	output := fs.String("output", "", "output file path (default: stdout)")
	_ = fs.Parse(args)

	if *importPath == "" {
		slog.Error("usage: go2nix generate-test-main --import-path PATH [--test-files FILES] [--xtest-files FILES] [--output FILE]")
		os.Exit(1)
	}

	opts := testmain.Options{
		ImportPath: *importPath,
	}
	if *testFiles != "" {
		opts.TestGoFiles = strings.Split(*testFiles, ",")
	}
	if *xtestFiles != "" {
		opts.XTestGoFiles = strings.Split(*xtestFiles, ",")
	}

	src, err := testmain.Generate(opts)
	if err != nil {
		slog.Error("generate-test-main failed", "err", err)
		os.Exit(1)
	}

	if *output != "" {
		if err := os.WriteFile(*output, src, 0o644); err != nil {
			slog.Error("writing output", "err", err)
			os.Exit(1)
		}
	} else {
		_, _ = os.Stdout.Write(src)
	}
}
