package main

import (
	"fmt"

	"example.com/testonly-embed/internal/greet"
)

func main() {
	fmt.Println(greet.Greet("world"))
}
