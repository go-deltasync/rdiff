// Package rdiff is a pure-Go, cgo-free, librsync-interoperable implementation of
// the rsync signature / delta / patch workflow: summarise a basis file as a
// Signature, compute a delta from that signature to a new file, and patch the
// basis with the delta to reconstruct the new file.
//
//	sig, _ := rdiff.GenerateSignature(basis, rdiff.DefaultBlockLen, 0, rdiff.Blake2SigMagic)
//	_ = rdiff.GenerateDelta(sig, newFile, deltaOut)
//	_ = rdiff.Patch(basisReaderAt, delta, out) // out == newFile
package rdiff

import (
	"io"

	impl "github.com/go-deltasync/rdiff/internal/rdiff"
)

// DefaultBlockLen is the default signature block length in bytes.
const DefaultBlockLen = impl.DefaultBlockLen

// Signature file-format magics, matching librsync. Blake2SigMagic selects the
// rollsum + BLAKE2b signature; MD4SigMagic selects the rollsum + MD4 signature.
const (
	Blake2SigMagic = impl.Blake2SigMagic
	MD4SigMagic    = impl.MD4SigMagic
)

// Signature holds the per-block weak and strong checksums of a basis file.
type Signature = impl.Signature

// GenerateSignature computes the signature of basis. blockLen is the block size
// in bytes (0 selects DefaultBlockLen); strongLen truncates the strong sum (0
// keeps the full width); magic selects the signature format.
func GenerateSignature(basis io.Reader, blockLen, strongLen int, magic uint32) (*Signature, error) {
	return impl.GenerateSignature(basis, blockLen, strongLen, magic)
}

// ReadSignature parses a serialised signature.
func ReadSignature(r io.Reader) (*Signature, error) {
	return impl.ReadSignature(r)
}

// GenerateDelta writes the delta that turns the signature's basis into next.
func GenerateDelta(sig *Signature, next io.Reader, out io.Writer) error {
	return impl.GenerateDelta(sig, next, out)
}

// Patch applies a delta to basis, writing the reconstructed file to out.
func Patch(basis io.ReaderAt, delta io.Reader, out io.Writer) error {
	return impl.Patch(basis, delta, out)
}
