# Experiment: go-asmgen on the Rollsum weak checksum

This branch dogfoods [go-asmgen](https://github.com/go-asmgen/asmgen) on a real
rdiff hot path — the Rollsum weak checksum inner loop (`Rollsum.Update`, an
Adler/Fletcher-style `s1`/`s2` accumulation with 16-bit wraparound).

- `weaksum_asmgen.go` — the go-asmgen generator (`go generate` / `go run`).
- `weaksum_arm64.s` — the generated Plan 9 assembly.
- `weaksum_asm.go` / `weaksum_generic.go` — arm64 decl + pure-Go fallback.
- `weaksum_asm_test.go` — correctness (bit-for-bit vs `WeakSum`) + benchmarks.

## Result

Correct: `weakSumASM` matches `WeakSum` for all lengths and the all-`0xFF`
overflow stress (the uint16 wraparound is reproduced exactly).

Performance (2048-byte block), Go vs asm:

| arch | runner | Go | ASM |
|---|---|---|---|
| arm64 | Apple Silicon (native) | ~1102 ns/op (1858 MB/s) | ~1075 ns/op (1904 MB/s) |
| amd64 | GitHub ubuntu (native) | ~1293 ns/op (1582 MB/s) | ~1292 ns/op (1585 MB/s) |

The hand-written scalar loop is ~2-3% faster than the compiler on arm64 and
**dead even** on amd64. Scalar asm rarely beats a modern compiler by much; a real
win needs **SIMD** (vectorized Adler-32). That is a separate, larger effort —
see below.

## What the exercise flushed out about go-asmgen

- **go-asmgen is an ABI0/layout helper, not an instruction-level DSL.** Here it
  contributed exactly: the frame layout (`block []byte` -> `block_base`/`block_len`,
  `uint32` result `$0-28`), the slice-field addressing, and the typed
  load/store mnemonics (`MOVBU` byte, `MOVW` result). *All* the compute — the
  loop, `ADD`/`AND`/`LSL`/`ORR`, the branches — is hand-written via `Raw`.
- **No multi-arch reuse for the compute.** The `Raw` body is arm64-specific;
  porting to amd64/riscv64/loong64 means rewriting it. Only the signature/layout
  is portable.
- **No correctness bugs.** uint32 return, slice args, unsigned byte loads, and
  the frame were all handled correctly the first time.
- Minor: generated labels are indented (cosmetic); `go generate` needs go-asmgen
  as a module require.


## On SIMD (the real opportunity — and where go-asmgen's scope ends)

A vectorized Adler-32 (NEON/SSE) would be the actual speedup (typically 4-8x).
Surveying it surfaced two concrete go-asmgen limits:

1. **`emit` produces only `TEXT` blocks — no `DATA`/`GLOBL`.** SIMD kernels almost
   always need a constant table (a weight vector `[16,15,...,1]`, a shuffle mask).
   go-asmgen cannot emit those; you would hand-write the `DATA`/`GLOBL` in a
   separate `.s` file, or synthesise the constant with instructions. This is a
   bounded, worthwhile feature to add to `emit`.
2. **go-asmgen contributes only the ABI0 layout.** The entire vector body
   (`VUADDLV`, `VUXTL`, the multiply-accumulate, the chunk loop) is hand-written
   `Raw`, and is arch-specific. So a SIMD kernel is ~95% hand assembly with
   go-asmgen handling the signature/frame.

Conclusion: the scalar dogfooding worked cleanly and is a fair demonstration; a
SIMD kernel is a real but separate undertaking that lives mostly outside what
go-asmgen models today.