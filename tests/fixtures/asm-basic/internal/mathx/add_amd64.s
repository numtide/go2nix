#include "textflag.h"

// func addAsm(a, b int64) int64
TEXT ·addAsm(SB), NOSPLIT, $0-24
	MOVQ a+0(FP), AX
	MOVQ b+8(FP), BX
	ADDQ BX, AX
	MOVQ AX, ret+16(FP)
	RET
