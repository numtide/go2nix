package main

import (
	"fmt"

	"example.com/cxx-pkgconfig/internal/snap"
)

func main() {
	fmt.Println(string(snap.Roundtrip([]byte("hello-snappy"))))
}
