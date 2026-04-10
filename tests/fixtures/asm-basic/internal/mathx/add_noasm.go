//go:build !amd64

package mathx

func addAsm(a, b int64) int64 { return a + b }
