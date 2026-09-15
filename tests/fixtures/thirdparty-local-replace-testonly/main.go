package main

import (
	"fmt"

	"example.com/thirdparty-local-replace-testonly/internal/greeter"
)

func main() {
	fmt.Println(greeter.Greet("world"))
}
