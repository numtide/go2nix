package main

import (
	"encoding/json"
	"flag"
	"go/build"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// LocalPkg describes a local package with its files and location.
type LocalPkg struct {
	ImportPath string `json:"import_path"`
	SrcDir     string `json:"src_dir"`
	PkgFiles          // embedded: go_files, cgo_files, s_files, etc., is_command, embed_cfg
}

func listLocalPackagesCmd(args []string) {
	flagSet := flag.NewFlagSet("list-local-packages", flag.ExitOnError)
	tagsFlag := flagSet.String("tags", "", "comma-separated build tags")
	flagSet.Parse(args)
	if flagSet.NArg() != 1 {
		log.Fatal("usage: go2nix list-local-packages [-tags=...] <module-root>")
	}
	root := flagSet.Arg(0)

	// 1. Parse go.mod for module path and local replace directives.
	goModData, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		log.Fatalf("reading go.mod: %v", err)
	}
	modFile, err := modfile.Parse("go.mod", goModData, nil)
	if err != nil {
		log.Fatalf("parsing go.mod: %v", err)
	}
	modulePath := modFile.Module.Mod.Path

	// Collect local replace targets: import path prefix → absolute directory.
	// A local replace has a relative path (no version) on the right side.
	localReplaces := map[string]string{} // import path → abs dir
	for _, rep := range modFile.Replace {
		if rep.New.Version == "" {
			// Local replace: resolve relative path from module root.
			absDir := rep.New.Path
			if !filepath.IsAbs(absDir) {
				absDir = filepath.Join(root, absDir)
			}
			absDir, _ = filepath.Abs(absDir)
			localReplaces[rep.Old.Path] = absDir
		}
	}

	// 2. Walk directories for Go packages.
	ctx := buildContext(*tagsFlag)
	pkgs := map[string]*LocalPkg{}
	localDeps := map[string][]string{}

	// Set of all known local import path prefixes (module path + local replaces).
	localPrefixes := []string{modulePath}
	for ip := range localReplaces {
		localPrefixes = append(localPrefixes, ip)
	}

	// walkDir discovers Go packages under dir and maps them using importBase as
	// the import path prefix (replacing relpath computation).
	walkDir := func(dir string, importBase string) {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == "vendor" || name == "testdata" || name == ".git" || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}

			pkg, importErr := ctx.ImportDir(path, build.IgnoreVendor)
			if importErr != nil {
				return nil // no Go files or build-constraint mismatch — skip
			}

			rel, _ := filepath.Rel(dir, path)
			importPath := importBase
			if rel != "." {
				importPath = importBase + "/" + rel
			}

			localPkg := &LocalPkg{
				ImportPath: importPath,
				SrcDir:     path,
				PkgFiles:   buildPkgFiles(pkg, path),
			}
			pkgs[importPath] = localPkg

			// Filter imports to local only.
			var local []string
			for _, imp := range pkg.Imports {
				if isLocalImport(imp, localPrefixes) {
					local = append(local, imp)
				}
			}
			localDeps[importPath] = local
			return nil
		})
	}

	// Walk the module root itself.
	walkDir(root, modulePath)

	// Walk each local replace target.
	for _, importPath := range slices.Sorted(maps.Keys(localReplaces)) {
		dir := localReplaces[importPath]
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			log.Fatalf("local replace target %s (%s) does not exist", importPath, dir)
		}
		walkDir(dir, importPath)
	}

	// 3. Topological sort.
	sorted := topoSort(pkgs, localDeps)

	// 4. JSON encode the sorted array to stdout.
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sorted); err != nil {
		log.Fatalf("encoding JSON: %v", err)
	}
}

// isLocalImport reports whether imp is a local import (under any of the given prefixes).
func isLocalImport(imp string, prefixes []string) bool {
	for _, p := range prefixes {
		if imp == p || strings.HasPrefix(imp, p+"/") {
			return true
		}
	}
	return false
}

// topoSort returns packages in dependency order using DFS post-order.
// Dependencies come before their dependents. Detects cycles.
func topoSort(pkgs map[string]*LocalPkg, localDeps map[string][]string) []*LocalPkg {
	var result []*LocalPkg
	visited := map[string]bool{}
	inStack := map[string]bool{}

	var visit func(string)
	visit = func(ip string) {
		if visited[ip] {
			return
		}
		if inStack[ip] {
			log.Fatalf("import cycle detected involving package %s", ip)
		}
		inStack[ip] = true
		for _, dep := range localDeps[ip] {
			visit(dep)
		}
		delete(inStack, ip)
		visited[ip] = true
		if pkg, ok := pkgs[ip]; ok {
			result = append(result, pkg)
		}
	}

	// Visit in sorted order for determinism.
	keys := slices.Sorted(maps.Keys(pkgs))
	for _, ip := range keys {
		visit(ip)
	}
	return result
}
