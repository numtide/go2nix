package mathx

// Implemented in add_amd64.s. The bodyless decl is what triggers the
// "missing function body" error if the .s file is dropped from the
// compile, so the fixture build itself is the regression check.
func addAsm(a, b int64) int64
