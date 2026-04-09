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
