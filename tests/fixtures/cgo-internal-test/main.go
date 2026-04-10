package main

import (
	"fmt"

	"example.com/cgo-internal-test/internal/adder"
)

func main() {
	fmt.Println(adder.Add(2, 3))
}
