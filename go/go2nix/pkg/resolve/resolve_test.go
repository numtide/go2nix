package resolve

import "testing"

func TestStoreDirOf(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"/nix/store/xxx-go/bin/go", "/nix/store/xxx-go"},
		{"/nix/store/abc123-cacert/etc/ssl/certs/ca-bundle.crt", "/nix/store/abc123-cacert"},
		{"/nix/store/zzz-go2nix/bin/go2nix", "/nix/store/zzz-go2nix"},
	}
	for _, tt := range tests {
		if got := storeDirOf(tt.input); got != tt.want {
			t.Errorf("storeDirOf(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCollectStdlibImports(t *testing.T) {
	graph := map[string]*ResolvedPkg{
		"a": {ImportPath: "a", Imports: []string{"b", "fmt", "net/http"}},
		"b": {ImportPath: "b", Imports: []string{"crypto/tls"}},
	}
	sorted := []*ResolvedPkg{graph["b"], graph["a"]}

	stdlib := collectStdlibImports(sorted, graph)

	// Should contain fmt, net/http, crypto/tls (sorted)
	if len(stdlib) != 3 {
		t.Fatalf("expected 3 stdlib imports, got %d: %v", len(stdlib), stdlib)
	}
	if stdlib[0] != "crypto/tls" || stdlib[1] != "fmt" || stdlib[2] != "net/http" {
		t.Errorf("stdlib = %v", stdlib)
	}
}
