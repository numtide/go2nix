package toposort

import (
	"strings"
	"testing"
)

func TestSort_Linear(t *testing.T) {
	nodes := map[string]string{"a": "A", "b": "B", "c": "C"}
	deps := map[string][]string{"a": {}, "b": {"a"}, "c": {"b"}}

	result, err := Sort(nodes, func(k string) []string { return deps[k] })
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
	// A before B before C
	if result[0] != "A" || result[1] != "B" || result[2] != "C" {
		t.Errorf("got %v", result)
	}
}

func TestSort_Cycle(t *testing.T) {
	nodes := map[string]string{"a": "A", "b": "B"}
	deps := map[string][]string{"a": {"b"}, "b": {"a"}}

	_, err := Sort(nodes, func(k string) []string { return deps[k] })
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected 'cycle' in error, got: %v", err)
	}
}

func TestSort_ExternalDeps(t *testing.T) {
	// "a" depends on "ext" which is not in nodes — should be skipped gracefully
	nodes := map[string]string{"a": "A"}
	deps := map[string][]string{"a": {"ext"}}

	result, err := Sort(nodes, func(k string) []string { return deps[k] })
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0] != "A" {
		t.Errorf("got %v", result)
	}
}

func TestSort_Empty(t *testing.T) {
	result, err := Sort(map[string]int{}, func(k string) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}
