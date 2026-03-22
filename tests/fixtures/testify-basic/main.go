package main

import (
	"fmt"

	"example.com/testify-basic/internal/greeter"
)

func main() {
	fmt.Println(greeter.Greet("world"))
}
