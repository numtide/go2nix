package main

import (
	"fmt"

	"example.com/mainsrc-precise/internal/embed"
	"example.com/mainsrc-precise/internal/greet"
)

func main() {
	fmt.Println(greet.Hello())
	fmt.Println(embed.Schema())
}
