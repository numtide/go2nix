package adder

import (
	_ "embed"
	"strconv"
	"strings"
	"testing"
)

//go:embed testdata/expected.txt
var expected string

func TestAdd(t *testing.T) {
	want, err := strconv.Atoi(strings.TrimSpace(expected))
	if err != nil {
		t.Fatalf("parsing embedded testdata/expected.txt: %v", err)
	}
	if got := Add(2, 3); got != want {
		t.Fatalf("Add(2,3) = %d, want %d", got, want)
	}
}

func TestBanner(t *testing.T) {
	if got := Banner(); got != "hello-from-embed" {
		t.Fatalf("Banner() = %q, want %q", got, "hello-from-embed")
	}
}
