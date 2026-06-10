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

Performance (Apple M-series, arm64, 2048-byte block):

| | ns/op | MB/s |
|---|---|---|
| `WeakSumGo`  | ~1102 | ~1858 |
| `WeakSumASM` | ~1075 | ~1904 |

The hand-written scalar loop is ~2-3% faster than the Go compiler's. Modest:
scalar asm rarely beats the compiler by much. A real win needs **SIMD**
(vectorized Adler-32), which is a separate, larger effort.

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
