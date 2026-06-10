//go:build ignore

// Command gen produces weaksum_{arm64,amd64}.s with go-asmgen — assembly
// versions of the Rollsum weak checksum (see rollsum.go) for benchmarking
// against pure Go. Run: go run weaksum_asmgen.go
//
// Note: go-asmgen lays out the ABI0 frame (block []byte -> block_base/block_len,
// uint32 result) and the typed byte/result moves; the loop and arithmetic are
// hand-written per architecture via Raw — there is no cross-arch reuse of the
// compute, only of the signature/layout.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/amd64"
	"github.com/go-asmgen/asmgen/arm64"
	"github.com/go-asmgen/asmgen/emit"
)

// func weakSumASM(block []byte) uint32
func sig() abi.Signature {
	return abi.LayoutArgs(
		[]abi.Arg{abi.Slice("block")},
		[]abi.Arg{abi.Scalar("ret", abi.Uint32)},
	)
}

func writeFile(path string, f *emit.File) {
	if err := os.WriteFile(path, []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", path)
}

func main() {
	// arm64: R0=ptr R1=count R2=s1 R3=s2 R4=b
	a := arm64.NewFunc("weakSumASM", sig(), 0)
	a.LoadArg("block_base", "R0").
		LoadArg("block_len", "R1").
		Raw("MOVD $0, R2").
		Raw("MOVD $0, R3").
		Raw("wsloop:").
		Raw("CBZ R1, wsdone").
		Raw("MOVBU (R0), R4").
		Raw("ADD $31, R4, R4").
		Raw("ADD R4, R2, R2").
		Raw("AND $0xFFFF, R2, R2").
		Raw("ADD R2, R3, R3").
		Raw("ADD $1, R0, R0").
		Raw("SUB $1, R1, R1").
		Raw("JMP wsloop").
		Raw("wsdone:").
		Raw("AND $0xFFFF, R3, R3").
		Raw("LSL $16, R3, R3").
		Raw("ORR R2, R3, R3").
		StoreRet("R3", "ret").
		Ret()
	fa := emit.NewFile("arm64")
	fa.Add(a.Func())
	writeFile("weaksum_arm64.s", fa)

	// amd64: AX=ptr BX=count CX=s1 DX=s2 SI=b (2-operand ISA)
	x := amd64.NewFunc("weakSumASM", sig(), 0)
	x.LoadArg("block_base", "AX").
		LoadArg("block_len", "BX").
		Raw("XORQ CX, CX").
		Raw("XORQ DX, DX").
		Raw("wsloop:").
		Raw("TESTQ BX, BX").
		Raw("JZ wsdone").
		Raw("MOVBQZX (AX), SI").
		Raw("ADDQ $31, SI").
		Raw("ADDQ SI, CX").
		Raw("ANDQ $0xFFFF, CX").
		Raw("ADDQ CX, DX").
		Raw("INCQ AX").
		Raw("DECQ BX").
		Raw("JMP wsloop").
		Raw("wsdone:").
		Raw("ANDQ $0xFFFF, DX").
		Raw("SHLQ $16, DX").
		Raw("ORQ CX, DX").
		StoreRet("DX", "ret").
		Ret()
	fx := emit.NewFile("amd64")
	fx.Add(x.Func())
	writeFile("weaksum_amd64.s", fx)
}
