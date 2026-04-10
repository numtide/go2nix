package main

// The direct cgo import makes this main package's compile write the
// .has_cgo marker; cmd/purebin (built next) must not inherit it.

/*
static int one(void) { return 1; }
*/
import "C"

import (
	"fmt"

	"example.com/cgo-internal-test/internal/adder"
)

func main() {
	fmt.Println(adder.Add(2, 3) * int(C.one()))
	fmt.Println(adder.Banner())
}
