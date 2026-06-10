//go:build ignore

// Command gen produces weaksum_amd64.s — an SSE2/SSSE3 vectorized Adler-style
// weak checksum — and weaksum_arm64.s (scalar; Go's arm64 assembler has no NEON
// integer multiply, so a vectorized Adler is impractical there). Run: go run.
//
// SSE algorithm (pure-byte Adler, +31 offset and mod 2^16 fixed up at the end):
// process 16-byte chunks; per chunk
//   byteSum  = PSADBW(v, 0)                       -> s1 += byteSum
//   weighted = horiz-sum PMADDUBSW(v, [16..1])    -> s2 += 16*s1_old + weighted
// then a scalar tail, then s1 += 31*L, s2 += 31*L*(L+1)/2, then mask to 16 bits.
package main

import (
	"fmt"
	"os"

	"github.com/go-asmgen/asmgen/abi"
	"github.com/go-asmgen/asmgen/amd64"
	"github.com/go-asmgen/asmgen/arm64"
	"github.com/go-asmgen/asmgen/emit"
)

func sig() abi.Signature {
	return abi.LayoutArgs(
		[]abi.Arg{abi.Slice("block")},
		[]abi.Arg{abi.Scalar("ret", abi.Uint32)},
	)
}

func main() {
	// ---- arm64: scalar (unchanged) ----
	a := arm64.NewFunc("weakSumASM", sig(), 0)
	a.LoadArg("block_base", "R0").LoadArg("block_len", "R1").
		Raw("MOVD $0, R2").Raw("MOVD $0, R3").
		Label("wsloop").
		Raw("CBZ R1, wsdone").
		LoadIndirect("R0", arm64.Uint8, "R4").
		Raw("ADD $31, R4, R4").Raw("ADD R4, R2, R2").Raw("AND $0xFFFF, R2, R2").
		Raw("ADD R2, R3, R3").Raw("ADD $1, R0, R0").Raw("SUB $1, R1, R1").
		Raw("JMP wsloop").
		Label("wsdone").
		Raw("AND $0xFFFF, R3, R3").Raw("LSL $16, R3, R3").Raw("ORR R2, R3, R3").
		StoreRet("R3", "ret").Ret()
	fa := emit.NewFile("arm64")
	fa.Add(a.Func())
	write("weaksum_arm64.s", fa)

	// ---- amd64: SSE SIMD ----
	fx := emit.NewFile("amd64")
	// tap = [16,15,...,1] signed bytes, the within-chunk position weights.
	tap := fx.Data("tap", []byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1})

	x := amd64.NewFunc("weakSumASM", sig(), 0)
	// AX=ptr  BX=len  CX=s1  DX=s2  SI=L (saved)  X7=tap  X6=zero
	x.LoadArg("block_base", "AX").LoadArg("block_len", "BX").
		Raw("MOVQ BX, SI").       // SI = L (for offset fixup)
		Raw("XORQ CX, CX").       // s1
		Raw("XORQ DX, DX").       // s2
		Raw("MOVOU %s+0(SB), X7", tap).
		Raw("PXOR X6, X6").       // zero
		// main 16-byte loop
		Label("simd").
		Raw("CMPQ BX, $16").
		Raw("JLT tail").
		Raw("MOVOU (AX), X0").    // v
		// byteSum -> R8
		Raw("MOVO X0, X1").
		Raw("PSADBW X6, X1").     // two u64 lane sums
		Raw("MOVO X1, X2").
		Raw("PSRLO $8, X2").      // high lane -> low
		Raw("PADDL X2, X1").      // X1.lo = byteSum
		Raw("MOVQ X1, R8").       // R8 = byteSum
		// weighted -> R9
		Raw("MOVO X0, X3").
		Raw("PMADDUBSW X7, X3").  // 8x u16: v[2i]*tap[2i]+v[2i+1]*tap[2i+1]
		Raw("MOVO X3, X4").Raw("PSRLO $8, X4").Raw("PADDW X4, X3").
		Raw("MOVO X3, X4").Raw("PSRLO $4, X4").Raw("PADDW X4, X3").
		Raw("MOVO X3, X4").Raw("PSRLO $2, X4").Raw("PADDW X4, X3"). // low u16 = weighted
		Raw("MOVQ X3, R9").
		Raw("ANDQ $0xFFFF, R9").  // weighted (16-bit)
		// fold: s2 += 16*s1 + weighted; s1 += byteSum
		Raw("MOVQ CX, R10").Raw("SHLQ $4, R10").  // 16*s1
		Raw("ADDQ R10, DX").Raw("ADDQ R9, DX").   // s2 += 16*s1 + weighted
		Raw("ADDQ R8, CX").                       // s1 += byteSum
		Raw("ADDQ $16, AX").Raw("SUBQ $16, BX").
		Raw("JMP simd").
		// scalar tail (< 16 bytes), pure bytes (no +31 yet)
		Label("tail").
		Raw("TESTQ BX, BX").
		Raw("JZ fixup").
		Raw("MOVBQZX (AX), R8").
		Raw("ADDQ R8, CX").       // s1 += b
		Raw("ADDQ CX, DX").       // s2 += s1
		Raw("INCQ AX").Raw("DECQ BX").
		Raw("JMP tail").
		// fixup: pure-byte s1/s2 -> add 31*L offset terms, then mask
		Label("fixup").
		// s1 += 31*L
		Raw("MOVQ SI, R8").Raw("IMULQ $31, R8").Raw("ADDQ R8, CX").
		// s2 += 31 * L*(L+1)/2
		Raw("MOVQ SI, R8").Raw("MOVQ SI, R9").Raw("INCQ R9").
		Raw("IMULQ R9, R8").Raw("SHRQ $1, R8").   // L*(L+1)/2
		Raw("IMULQ $31, R8").Raw("ADDQ R8, DX").
		// mask & pack
		Raw("ANDQ $0xFFFF, CX").
		Raw("ANDQ $0xFFFF, DX").
		Raw("SHLQ $16, DX").
		Raw("ORQ CX, DX").
		StoreRet("DX", "ret").Ret()
	fx.Add(x.Func())
	write("weaksum_amd64.s", fx)
}

func write(path string, f *emit.File) {
	if err := os.WriteFile(path, []byte(f.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", path)
}
