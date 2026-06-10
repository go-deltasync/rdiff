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

| arch | kind | runner | Go | ASM | speedup |
|---|---|---|---|---|---|
| arm64 | scalar | Apple Silicon (native) | ~1102 ns/op (1858 MB/s) | ~1075 ns/op (1904 MB/s) | ~1.03x |
| amd64 | scalar | GitHub ubuntu (native) | ~1293 ns/op (1582 MB/s) | ~1292 ns/op (1585 MB/s) | ~1.00x |
| amd64 | **SSE SIMD** | GitHub ubuntu (native) | ~1480 ns/op (1.38 GB/s) | **~230 ns/op (8.9 GB/s)** | **~6.4x** |

Scalar asm barely moves the needle (the compiler is good). The **SSE-vectorized
Adler is ~6.4x faster** than the scalar Go — the real win, and the payoff of the
whole exercise.

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


## SIMD — done on amd64 (~6.4x), blocked on arm64

A vectorized Adler is the real speedup, and on amd64 it lands big:

- **amd64 (SSE2/SSSE3):** per 16-byte chunk, `PSADBW` gives the byte sum and
  `PMADDUBSW(v, [16..1])` the position-weighted sum (reduced with `PSRLO`/`PADDW`);
  a scalar fold maintains s1/s2, the `+31` offset and mod-2^16 are fixed up at the
  end. **~6.4x faster than scalar Go, bit-for-bit correct.** The `[16..1]` weight
  table is emitted by go-asmgen's `emit.File.Data`.
- **arm64 (NEON): not done** — Go's arm64 assembler has **no NEON integer
  multiply** (`VMUL`/`VMLA`/`VUMULL` are absent from the assembler), so the
  multiply-based Adler is impractical without a fiddly multiply-free
  column-transpose. That is a `cmd/asm` limitation, not go-asmgen's.

### What go-asmgen contributed, and what it didn't

- **Did:** the ABI0 layout (`block []byte` -> `block_base`/`block_len`, `uint32`
  result), the typed scalar moves (`LoadIndirect`, `StoreRet`), `Label`, and —
  crucially — the `DATA`/`GLOBL` weight table via `emit.File.Data`. Without that
  last piece (added in v0.4.0) the SSE kernel could not have been generated.
- **Didn't:** the vector body itself (`PSADBW`/`PMADDUBSW`/…) is hand-written
  `Raw` and amd64-specific. go-asmgen handles the boilerplate and the constant
  table; the kernel is yours. That is the intended division of labour.
