package main

import (
	"testing"

	"example.com/test-helper-pkg/internal/unbuiltdep"
)

func TestAnswer(t *testing.T) {
	if got := unbuiltdep.Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
}
