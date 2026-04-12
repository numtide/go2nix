package sib_test

import (
	"testing"

	"example.com/sib/testutil"
)

func TestGreet(t *testing.T) {
	if got := testutil.MustGreet("world"); got != "hello world" {
		t.Fatalf("Greet = %q", got)
	}
}
