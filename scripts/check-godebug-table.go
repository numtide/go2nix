// check-godebug-table verifies (or updates) the godebugTable in
// go/go2nix/pkg/buildinfo/godebug.go against the upstream Go toolchain's
// internal/godebugs/table.go.
//
// Usage:
//
//	go run scripts/check-godebug-table.go              # check mode (default)
//	go run scripts/check-godebug-table.go --update     # rewrite godebug.go
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
	Removed int // minor version that retired the setting (0 = still in godebugs.All)
}

func main() {
	update := len(os.Args) > 1 && os.Args[1] == "--update"

	goroot, err := goRoot()
	if err != nil {
		fatal("cannot determine GOROOT: %v", err)
	}
	toolchainMinor, err := goMinor(goroot)
	if err != nil {
		fatal("cannot determine toolchain version: %v", err)
	}

	upstreamPath := filepath.Join(goroot, "src", "internal", "godebugs", "table.go")
	upstream, err := parseUpstream(upstreamPath)
	if err != nil {
		fatal("parsing upstream table: %v", err)
	}
	removed, err := parseRemoved(upstreamPath)
	if err != nil {
		fatal("parsing upstream removed table: %v", err)
	}

	godebugPath := filepath.Join("go", "go2nix", "pkg", "buildinfo", "godebug.go")
	committed, err := parseCommitted(godebugPath)
	if err != nil {
		fatal("parsing committed table: %v", err)
	}

	// A retired setting keeps its pre-removal Changed/Old (upstream drops
	// them from godebugs.Removed) so older toolchains still get the right
	// default; the committed table is the only place those values survive.
	upstream = append(upstream, carryRemoved(removed, committed)...)
	// Likewise keep committed rows this toolchain cannot see: settings a
	// newer toolchain added or retired. Updating with an older toolchain
	// must never erase what a newer one recorded.
	cm := make(map[string]entry, len(committed))
	for _, e := range committed {
		cm[e.Name] = e
	}
	for i, e := range upstream {
		// Live for this toolchain but retired by a newer one: keep that
		// newer toolchain's Removed.
		if c, ok := cm[e.Name]; ok && e.Removed == 0 && c.Removed > toolchainMinor {
			upstream[i].Removed = c.Removed
		}
		delete(cm, e.Name)
	}
	for _, e := range cm {
		if e.Changed > toolchainMinor || (e.Removed > 0 && e.Removed > toolchainMinor) {
			upstream = append(upstream, e)
		}
	}
	sort.Slice(upstream, func(i, j int) bool { return upstream[i].Name < upstream[j].Name })

	if update {
		if err := rewriteTable(godebugPath, upstream); err != nil {
			fatal("updating table: %v", err)
		}
		fmt.Printf("Updated %s (%d entries)\n", godebugPath, len(upstream))
		return
	}

	// Check mode. The committed table is a superset over toolchains: it
	// carries settings newer toolchains added (Changed > this one) and
	// settings this toolchain already retired (Removed set), both of which
	// this toolchain's table.go cannot vouch for. Only disagreements it can
	// see are stale.
	diff := diffTables(committed, upstream, toolchainMinor)
	if diff == "" {
		fmt.Printf("OK: godebugTable consistent with go1.%d's table.go (%d committed entries)\n", toolchainMinor, len(committed))
		return
	}

	fmt.Fprintf(os.Stderr, "godebugTable is stale.\n\n%s\n", diff)
	fmt.Fprintf(os.Stderr, "Run 'go run scripts/check-godebug-table.go --update' to fix.\n")
	os.Exit(1)
}

// goMinor reports the minor version of the toolchain at goroot from its
// VERSION file ("go1.27.0", "go1.27rc1", ...).
func goMinor(goroot string) (int, error) {
	data, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	if err != nil {
		return 0, err
	}
	v := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	v = strings.TrimPrefix(v, "go")
	if !strings.HasPrefix(v, "1.") {
		return 0, fmt.Errorf("unexpected VERSION %q", v)
	}
	rest := v[len("1."):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	return strconv.Atoi(rest[:end])
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

// parseRemoved parses internal/godebugs/table.go's Removed list (Go >= 1.24)
// and returns Name/Removed pairs. Old there is a predicate, not a value, so
// it is not extracted; see carryRemoved.
func parseRemoved(path string) ([]entry, error) {
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
			if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "Removed" || len(vs.Values) != 1 {
				continue
			}
			comp, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range comp.Elts {
				if e, ok := extractEntry(elt); ok && e.Removed > 0 {
					entries = append(entries, e)
				}
			}
		}
	}
	return entries, nil
}

// carryRemoved turns upstream's Removed list into full entries by taking
// Changed/Old from the committed table. A retired name the committed table
// never carried is skipped (benign, see the godebugTable comment) with a
// notice, since its pre-removal default is not recoverable from the
// current toolchain.
func carryRemoved(removed, committed []entry) []entry {
	cm := make(map[string]entry, len(committed))
	for _, e := range committed {
		cm[e.Name] = e
	}
	var out []entry
	for _, r := range removed {
		c, ok := cm[r.Name]
		if !ok || c.Changed == 0 {
			fmt.Fprintf(os.Stderr, "note: removed setting %q has no committed Changed/Old; skipping\n", r.Name)
			continue
		}
		out = append(out, entry{Name: r.Name, Changed: c.Changed, Old: c.Old, Removed: r.Removed})
	}
	return out
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
		case "Removed":
			lit, ok := kv.Value.(*ast.BasicLit)
			if ok && lit.Kind == token.INT {
				e.Removed, _ = strconv.Atoi(lit.Value)
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
		if e.Removed > 0 {
			fmt.Fprintf(&b, "\t{Name: %q, Changed: %d, Old: %q, Removed: %d},\n", e.Name, e.Changed, e.Old, e.Removed)
			continue
		}
		fmt.Fprintf(&b, "\t{Name: %q, Changed: %d, Old: %q},\n", e.Name, e.Changed, e.Old)
	}
	return b.String()
}

// diffTables compares the committed superset table against what the
// toolchain at toolchainMinor can see: its godebugs.All entries (with
// Removed == 0) and its godebugs.Removed names (Removed > 0, Changed/Old
// carried from the committed table, so only Removed is upstream's).
func diffTables(committed, upstream []entry, toolchainMinor int) string {
	cm := make(map[string]entry, len(committed))
	for _, e := range committed {
		cm[e.Name] = e
	}
	um := make(map[string]entry, len(upstream))
	for _, e := range upstream {
		um[e.Name] = e
	}

	var buf strings.Builder

	// Missing from committed: every live or retired setting this toolchain
	// knows must be present.
	for _, e := range upstream {
		if _, ok := cm[e.Name]; !ok {
			fmt.Fprintf(&buf, "  + missing: {Name: %q, Changed: %d, Old: %q, Removed: %d}\n", e.Name, e.Changed, e.Old, e.Removed)
		}
	}
	// Extra in committed: only stale if this toolchain should know it, i.e.
	// it was changed on or before this toolchain and not retired by it.
	for _, e := range committed {
		if _, ok := um[e.Name]; ok {
			continue
		}
		if e.Changed > toolchainMinor || (e.Removed > 0 && e.Removed <= toolchainMinor) {
			continue
		}
		fmt.Fprintf(&buf, "  - extra:   {Name: %q, Changed: %d, Old: %q, Removed: %d}\n", e.Name, e.Changed, e.Old, e.Removed)
	}
	// Disagreements on settings both sides know.
	for _, u := range upstream {
		c, ok := cm[u.Name]
		if !ok {
			continue
		}
		if u.Removed > 0 {
			// Retired here: this toolchain vouches only for the Removed version.
			if c.Removed != u.Removed {
				fmt.Fprintf(&buf, "  ~ changed: %q: committed Removed: %d, upstream Removed: %d\n", u.Name, c.Removed, u.Removed)
			}
			continue
		}
		// Live here: Changed/Old must agree; a committed Removed from a NEWER
		// toolchain is fine, one at or below this toolchain contradicts All.
		if c.Changed != u.Changed || c.Old != u.Old || (c.Removed > 0 && c.Removed <= toolchainMinor) {
			fmt.Fprintf(&buf, "  ~ changed: %q: committed={Changed: %d, Old: %q, Removed: %d} upstream={Changed: %d, Old: %q}\n",
				u.Name, c.Changed, c.Old, c.Removed, u.Changed, u.Old)
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
