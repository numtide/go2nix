package main

import (
	"fmt"

	"example.com/xtest-local-dep/internal/handler"
	"example.com/xtest-local-dep/internal/server"
)

func main() {
	fmt.Println(server.Start())
	fmt.Println(handler.Handle("hello"))
}
