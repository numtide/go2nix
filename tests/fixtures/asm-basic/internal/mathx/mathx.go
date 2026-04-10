// Package mathx exposes a function implemented in assembly on amd64,
// to exercise compileWithAsm (-gensymabis, -symabis, -asmhdr, asm arch
// defines, pack append). The decl below has no body — the implementation
// is in add_amd64.s.
package mathx

func Add(a, b int64) int64 {
	return addAsm(a, b)
}
