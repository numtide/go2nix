package main

import (
	"fmt"

	"example.com/dual-cgo/internal/dualcgo"
)

func main() {
	fmt.Println(dualcgo.Mode())
}
