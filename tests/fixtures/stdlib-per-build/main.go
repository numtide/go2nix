// Reaches the standard-library packages whose files depend on CGO_ENABLED
// or on a build tag: net (netgo), os/user (osusergo), time (timetzdata).
package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"time"

	"example.com/stdlib-per-build/internal/dualcgo"
)

// lookups keeps the resolver, the user lookup and the time zone loader
// reachable for the linker.
func lookups(name string) {
	fmt.Println(net.LookupHost(name))
	fmt.Println(user.Lookup(name))
	fmt.Println(time.LoadLocation(name))
}

func main() {
	fmt.Println(dualcgo.Mode())
	if len(os.Args) > 1 {
		lookups(os.Args[1])
	}
}
