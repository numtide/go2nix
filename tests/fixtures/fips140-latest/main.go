package main

import (
	"crypto/sha256"
	"fmt"
)

func main() {
	fmt.Printf("%x\n", sha256.Sum256([]byte("hello")))
}
