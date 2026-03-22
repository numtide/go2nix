package app

import (
	"testing"

	"example.com/test-helper-pkg/internal/testutil"
)

func TestRun(t *testing.T) {
	testutil.AssertOK(t, Run())
}
