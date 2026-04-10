// Package snap links libsnappy.so via `#cgo pkg-config: snappy` and a C++
// shim (shim.cc) that calls snappy::Compress / snappy::Uncompress. Exercises
// the full chain: packageOverrides.nativeBuildInputs → resolvePkgConfig →
// compileCgo CXXFiles path → transitive cxx=true → -extld $CXX → external
// linker against a real .so. cxx-cgo proves the CXX-as-extld path with an
// in-tree .cc only; this proves the same path also works when the C++
// dependency is an external pkg-config-supplied library.
package snap

// #cgo pkg-config: snappy
// #include <stdlib.h>
// int snap_roundtrip(const char* in, size_t in_len, char** out, size_t* out_len);
import "C"
import "unsafe"

func Roundtrip(in []byte) []byte {
	var out *C.char
	var outLen C.size_t
	cin := (*C.char)(unsafe.Pointer(unsafe.SliceData(in)))
	if C.snap_roundtrip(cin, C.size_t(len(in)), &out, &outLen) != 0 {
		panic("snap_roundtrip failed")
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoBytes(unsafe.Pointer(out), C.int(outLen))
}
