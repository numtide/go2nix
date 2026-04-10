//go:build cgo

package dualcgo

// #include <stdlib.h>
import "C"

func Mode() string { return "cgo" }
