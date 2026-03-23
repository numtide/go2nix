// check-godebug-table verifies (or updates) the godebugTable in
// go/go2nix/pkg/buildinfo/godebug.go against the upstream Go toolchain's
// internal/godebugs/table.go.
//
// Usage:
//
//	go run .github/scripts/check-godebug-table.go              # check mode (default)
//	go run .github/scripts/check-godebug-table.go --update     # rewrite godebug.go
//
//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type entry struct {
	Name    string
	Changed int
	Old     string
}

func main() {
	update := len(os.Args) > 1 && os.Args[1] == "--update"

	goroot, err := goRoot()
	if err != nil {
		fatal("cannot determine GOROOT: %v", err)
	}

	upstreamPath := filepath.Join(goroot, "src", "internal", "godebugs", "table.go")
	upstream, err := parseUpstream(upstreamPath)
	if err != nil {
		fatal("parsing upstream table: %v", err)
	}

	godebugPath := filepath.Join("go", "go2nix", "pkg", "buildinfo", "godebug.go")
	committed, err := parseCommitted(godebugPath)
	if err != nil {
		fatal("parsing committed table: %v", err)
	}

	if update {
		if err := rewriteTable(godebugPath, upstream); err != nil {
			fatal("updating table: %v", err)
		}
		fmt.Printf("Updated %s (%d entries)\n", godebugPath, len(upstream))
		return
	}

	// Check mode.
	diff := diffTables(committed, upstream)
	if diff == "" {
		fmt.Printf("OK: godebugTable matches upstream (%d entries)\n", len(upstream))
		return
	}

	fmt.Fprintf(os.Stderr, "godebugTable is stale.\n\n%s\n", diff)
	fmt.Fprintf(os.Stderr, "Run 'go run .github/scripts/check-godebug-table.go --update' to fix.\n")
	os.Exit(1)
}

func goRoot() (string, error) {
	if gr := os.Getenv("GOROOT"); gr != "" {
		return gr, nil
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseUpstream parses internal/godebugs/table.go and returns entries with Changed > 0.
func parseUpstream(path string) ([]entry, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var entries []entry
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "All" {
				continue
			}
			if len(vs.Values) != 1 {
				continue
			}
			comp, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range comp.Elts {
				e, ok := extractEntry(elt)
				if ok && e.Changed > 0 {
					entries = append(entries, e)
				}
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func extractEntry(expr ast.Expr) (entry, bool) {
	comp, ok := expr.(*ast.CompositeLit)
	if !ok {
		return entry{}, false
	}
	var e entry
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			lit, ok := kv.Value.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				e.Name, _ = strconv.Unquote(lit.Value)
			}
		case "Changed":
			lit, ok := kv.Value.(*ast.BasicLit)
			if ok && lit.Kind == token.INT {
				e.Changed, _ = strconv.Atoi(lit.Value)
			}
		case "Old":
			lit, ok := kv.Value.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				e.Old, _ = strconv.Unquote(lit.Value)
			}
		}
	}
	return e, e.Name != ""
}

// parseCommitted extracts the godebugTable entries from the committed file.
func parseCommitted(path string) ([]entry, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var entries []entry
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "godebugTable" {
				continue
			}
			if len(vs.Values) != 1 {
				continue
			}
			comp, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range comp.Elts {
				e, ok := extractEntry(elt)
				if ok {
					entries = append(entries, e)
				}
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func renderTable(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "\t{Name: %q, Changed: %d, Old: %q},\n", e.Name, e.Changed, e.Old)
	}
	return b.String()
}

func diffTables(committed, upstream []entry) string {
	cm := make(map[string]entry, len(committed))
	for _, e := range committed {
		cm[e.Name] = e
	}
	um := make(map[string]entry, len(upstream))
	for _, e := range upstream {
		um[e.Name] = e
	}

	var buf strings.Builder

	// Missing from committed.
	for _, e := range upstream {
		if _, ok := cm[e.Name]; !ok {
			fmt.Fprintf(&buf, "  + missing: {Name: %q, Changed: %d, Old: %q}\n", e.Name, e.Changed, e.Old)
		}
	}
	// Extra in committed.
	for _, e := range committed {
		if _, ok := um[e.Name]; !ok {
			fmt.Fprintf(&buf, "  - extra:   {Name: %q, Changed: %d, Old: %q}\n", e.Name, e.Changed, e.Old)
		}
	}
	// Changed values.
	for _, u := range upstream {
		c, ok := cm[u.Name]
		if !ok {
			continue
		}
		if c.Changed != u.Changed || c.Old != u.Old {
			fmt.Fprintf(&buf, "  ~ changed: %q: committed={Changed: %d, Old: %q} upstream={Changed: %d, Old: %q}\n",
				u.Name, c.Changed, c.Old, u.Changed, u.Old)
		}
	}

	return buf.String()
}

// rewriteTable replaces the godebugTable literal elements using AST positions.
// It locates the composite literal via go/parser, then splices new content
// between the opening brace and closing brace using token offsets.
func rewriteTable(path string, entries []entry) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	var comp *ast.CompositeLit
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "godebugTable" {
				continue
			}
			if len(vs.Values) == 1 {
				comp, _ = vs.Values[0].(*ast.CompositeLit)
			}
		}
	}
	if comp == nil {
		return fmt.Errorf("cannot find godebugTable composite literal in %s", path)
	}

	// Lbrace is the position of '{', Rbrace is '}'.
	// Replace everything between them (exclusive of both braces).
	lbrace := fset.Position(comp.Lbrace).Offset
	rbrace := fset.Position(comp.Rbrace).Offset

	var buf bytes.Buffer
	buf.Write(data[:lbrace+1]) // up to and including '{'
	buf.WriteByte('\n')
	buf.WriteString(renderTable(entries))
	buf.Write(data[rbrace:]) // from '}' onward

	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
