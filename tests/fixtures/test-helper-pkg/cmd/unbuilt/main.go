// Binary unbuilt is intentionally NOT in dag.nix's subPackages, so neither
// it nor its dependency unbuiltdep have a compile derivation.
package main

import (
	"fmt"

	"example.com/test-helper-pkg/internal/unbuiltdep"
)

func main() { fmt.Println(unbuiltdep.Answer()) }
