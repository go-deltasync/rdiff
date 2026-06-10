//go:build ignore

// Command gen produces weaksum_arm64.s with go-asmgen — an assembly version of
// the Rollsum weak checksum (see rollsum.go) for benchmarking against the pure-Go
// implementation. Run: go run weaksum_asmgen.go
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/arm64"
	"github.com/go-asmgen/asmgen/emit"
)

func main() {
	// func weakSumASM(block []byte) uint32
	sig := abi.LayoutArgs(
		[]abi.Arg{abi.Slice("block")},
		[]abi.Arg{abi.Scalar("ret", abi.Uint32)},
	)
	b := arm64.NewFunc("weakSumASM", sig, 0)
	b.LoadArg("block_base", "R0"). // ptr
		LoadArg("block_len", "R1"). // count
		Raw("MOVD $0, R2").         // s1
		Raw("MOVD $0, R3").         // s2
		Raw("wsloop:").
		Raw("CBZ R1, wsdone").
		Raw("MOVBU (R0), R4").      // b := block[i]
		Raw("ADD $31, R4, R4").     // b + RS_CHAR_OFFSET
		Raw("ADD R4, R2, R2").      // s1 += b+31
		Raw("AND $0xFFFF, R2, R2"). // s1 &= 0xFFFF  (uint16 wraparound)
		Raw("ADD R2, R3, R3").      // s2 += s1
		Raw("ADD $1, R0, R0").      // ptr++
		Raw("SUB $1, R1, R1").      // count--
		Raw("JMP wsloop").
		Raw("wsdone:").
		Raw("AND $0xFFFF, R3, R3"). // s2 &= 0xFFFF
		Raw("LSL $16, R3, R3").     // s2 << 16
		Raw("ORR R2, R3, R3").      // | s1
		StoreRet("R3", "ret").
		Ret()

	f := emit.NewFile("arm64")
	f.Add(b.Func())
	if err := os.WriteFile("weaksum_arm64.s", []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote weaksum_arm64.s")
}
