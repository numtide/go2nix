// Package cxxdep is a cgo package whose native side is C++ (concat.cc),
// so the link must use CXX as -extld. The main package is pure Go and
// only imports this package — exercises the *transitive* CXXFiles walk
// in nix/dag (mirroring cmd/go gcToolchain.ld) rather than relying on
// CXXFiles in the main package itself.
package cxxdep

// #include <stdlib.h>
// char* cxx_concat(const char* a, const char* b);
import "C"
import "unsafe"

func Concat(a, b string) string {
	ca, cb := C.CString(a), C.CString(b)
	defer C.free(unsafe.Pointer(ca))
	defer C.free(unsafe.Pointer(cb))
	cr := C.cxx_concat(ca, cb)
	defer C.free(unsafe.Pointer(cr))
	return C.GoString(cr)
}
