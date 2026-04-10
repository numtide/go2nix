package main

import (
	"fmt"

	"example.com/cxx-cgo/internal/cxxdep"
)

func main() {
	fmt.Println(cxxdep.Concat("hello-", "cxx"))
}
