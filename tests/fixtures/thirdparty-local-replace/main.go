package main

import (
	"fmt"

	"github.com/stretchr/testify/assert"
)

func main() {
	if assert.ObjectsAreEqual("a", "a") {
		fmt.Println("ok")
	}
}
