package greeter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGreet(t *testing.T) {
	assert.Equal(t, "hello world", Greet("world"))
}
