<p align="center"><img src="https://raw.githubusercontent.com/go-deltasync/brand/main/social/go-deltasync.png" alt="go-deltasync/rdiff" width="720"></p>

# rdiff

[![ci](https://github.com/go-deltasync/rdiff/actions/workflows/ci.yml/badge.svg)](https://github.com/go-deltasync/rdiff/actions/workflows/ci.yml)
[![compat](https://github.com/go-deltasync/rdiff/actions/workflows/compat.yml/badge.svg)](https://github.com/go-deltasync/rdiff/actions/workflows/compat.yml)
[![coverage](https://img.shields.io/badge/coverage-100%25-brightgreen)](https://github.com/go-deltasync/rdiff/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-deltasync/rdiff.svg)](https://pkg.go.dev/github.com/go-deltasync/rdiff)
[![Go version](https://img.shields.io/github/go-mod/go-version/go-deltasync/rdiff)](go.mod)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)

A pure-Go, **librsync-interoperable** implementation of the classic rsync
signature / delta / patch workflow. Part of the
[`go-deltasync`](https://github.com/go-deltasync) family of delta-sync tools.

```
signature  BASIS         -> SIGNATURE   weak + strong checksums of every block
delta      SIGNATURE NEW  -> DELTA       instructions to turn BASIS into NEW
patch      BASIS DELTA    -> NEW         apply the instructions
```

The on-disk formats match librsync exactly (delta magic `0x72730236`,
signature magics `0x72730136` MD4 / `0x72730137` BLAKE2b), so files are
interchangeable with the C `rdiff`.

## Install

```sh
go install github.com/go-deltasync/rdiff/cmd/rdiff@latest
```

## Usage

```sh
rdiff signature old.bin old.sig          # default: BLAKE2b, 2048-byte blocks
rdiff signature -H md4 old.bin old.sig   # MD4 strong sum
rdiff delta old.sig new.bin patch.delta
rdiff patch old.bin patch.delta out.bin
cmp new.bin out.bin                      # identical
```

`-` means stdin/stdout for the streamable argument of each command. `patch`'s
BASIS must be a real file, since COPY commands read it at random offsets.

## How it works

A rolling weak checksum (librsync's Rollsum, `RS_CHAR_OFFSET = 31`) slides
byte-by-byte over the new file. On a weak-sum hit, a strong sum (MD4 or
BLAKE2b-256, optionally truncated) confirms a real block match, emitted as a
COPY referencing the basis; unmatched runs become LITERALs.

### Current limitations

- **Short block matched only at the tail.** Full `BlockLen`-sized basis blocks
  are matched anywhere; the basis's short final block is matched only when the
  new file *ends* with those bytes (a short block appearing mid-stream is
  emitted literally). Output is always correct, just not always maximally
  compact.
- Delta generation loads the target file into memory.

## Library

Importable for use in other Go programs (pure Go, no cgo):

```go
import "github.com/go-deltasync/rdiff"

sig, _ := rdiff.GenerateSignature(basis, rdiff.DefaultBlockLen, 0, rdiff.Blake2SigMagic)
_ = rdiff.GenerateDelta(sig, newFile, deltaOut)
_ = rdiff.Patch(basisReaderAt, delta, out) // out == newFile
```

## License

BSD-3-Clause. See [LICENSE](LICENSE).
