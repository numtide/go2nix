// Binary lang-loopvar is the regression fixture for -lang threading.
//
// The //go:debug directive below is the regression fixture for source-file
// godebug parsing: with go 1.21 the table-derived DefaultGODEBUG would not
// include panicnil (Changed=21), so its presence proves the directive was
// honoured.
//
//go:debug panicnil=1
package main

import (
	"fmt"
	"os"

	"example.com/langloopvar/internal/loop"
)

func main() {
	got := loop.Capture()
	want := []int{3, 3, 3} // go1.21 shared-loopvar semantics
	if len(got) != len(want) {
		fmt.Printf("FAIL: got %v want %v\n", got, want)
		os.Exit(1)
	}
	for i := range want {
		if got[i] != want[i] {
			fmt.Printf("FAIL: loopvar semantics mismatch: got %v want %v "+
				"(non-root subpackage compiled without -lang=go1.21?)\n", got, want)
			os.Exit(1)
		}
	}
	fmt.Println("PASS: go1.21 loopvar semantics preserved in non-root subpackage")
}
