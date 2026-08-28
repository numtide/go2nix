package lib

import "testing"

func TestGreeting(t *testing.T) {
	if Greeting() != "hello srcfilter" {
		t.Fatal("unexpected greeting")
	}
}
