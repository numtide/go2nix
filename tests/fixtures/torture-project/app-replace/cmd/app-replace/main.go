package main

import (
	"fmt"

	"go.uber.org/atomic"
)

func main() {
	fmt.Println(atomic.NewInt64(42).Load())
}
