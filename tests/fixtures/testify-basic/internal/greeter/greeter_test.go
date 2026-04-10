package greeter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGreet(t *testing.T) {
	assert.Equal(t, "hello world", Greet("world"))
}

// TestSkipMe must NOT run — dag.nix sets checkFlags=["-test.run=^TestGreet$"].
// If checkFlags don't reach the testrunner this fails the build.
func TestSkipMe(t *testing.T) {
	t.Fatal("checkFlags -test.run did not reach the test binary")
}
