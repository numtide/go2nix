package a

import "testing"

func TestGreeting(t *testing.T) {
	if got := Greeting(); got != "ok" {
		t.Fatalf("Greeting() = %q, want %q", got, "ok")
	}
}
