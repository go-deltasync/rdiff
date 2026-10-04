package rdiff

import (
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/md4" //nolint:staticcheck // librsync wire format requires MD4
)

// librsync file magics (big-endian uint32 at the start of each file).
const (
	// DeltaMagic prefixes every delta stream.
	DeltaMagic uint32 = 0x72730236
	// MD4SigMagic prefixes a signature whose strong sum is (truncated) MD4.
	MD4SigMagic uint32 = 0x72730136
	// Blake2SigMagic prefixes a signature whose strong sum is (truncated) BLAKE2b.
	Blake2SigMagic uint32 = 0x72730137
)

// Strong-sum sizing, per librsync.
const (
	md4SumLength    = 16 // full MD4 digest
	blake2SumLength = 32 // librsync uses BLAKE2b-256
)

// DefaultBlockLen is librsync's default signature block size.
const DefaultBlockLen = 2048

// Block is one entry of a signature: the weak and (truncated) strong checksum
// of a single block of the basis file.
type Block struct {
	Weak   uint32
	Strong []byte
}

// Signature is the parsed form of a librsync signature file plus an index that
// makes weak-sum lookups O(1) during delta generation.
type Signature struct {
	Magic     uint32
	BlockLen  int
	StrongLen int
	Blocks    []Block

	// weakIndex maps a weak sum to the indices of every block sharing it.
	weakIndex map[uint32][]int
}

// strongSum returns the (untruncated) strong checksum of block under the hash
// implied by the signature magic.
func strongSumFor(magic uint32, block []byte) ([]byte, error) {
	switch magic {
	case MD4SigMagic:
		h := md4.New()
		_, _ = h.Write(block)
		return h.Sum(nil), nil
	case Blake2SigMagic:
		sum := blake2b.Sum256(block)
		return sum[:], nil
	default:
		return nil, fmt.Errorf("rdiff: unknown signature magic %#08x", magic)
	}
}

// fullStrongLen is the untruncated digest length for a magic.
func fullStrongLen(magic uint32) (int, error) {
	switch magic {
	case MD4SigMagic:
		return md4SumLength, nil
	case Blake2SigMagic:
		return blake2SumLength, nil
	default:
		return 0, fmt.Errorf("rdiff: unknown signature magic %#08x", magic)
	}
}

// GenerateSignature reads all of basis and produces its signature.
//
// blockLen<=0 selects DefaultBlockLen. strongLen<=0 selects the full digest
// length for the chosen hash; otherwise the strong sum is truncated to
// strongLen bytes (matching librsync's -S option). magic selects the hash.
//
// As in librsync, the final short block (if any) is summed over its actual
// bytes only — it is NOT zero-padded (this differs from zsync).
func GenerateSignature(basis io.Reader, blockLen, strongLen int, magic uint32) (*Signature, error) {
	if blockLen <= 0 {
		blockLen = DefaultBlockLen
	}
	full, err := fullStrongLen(magic)
	if err != nil {
		return nil, err
	}
	if strongLen <= 0 || strongLen > full {
		strongLen = full
	}

	sig := &Signature{
		Magic:     magic,
		BlockLen:  blockLen,
		StrongLen: strongLen,
		weakIndex: make(map[uint32][]int),
	}

	buf := make([]byte, blockLen)
	for {
		n, err := io.ReadFull(basis, buf)
		if n > 0 {
			block := buf[:n]
			weak := WeakSum(block)
			// magic was already validated by fullStrongLen above, so this
			// cannot fail here.
			strong, _ := strongSumFor(magic, block)
			st := make([]byte, strongLen)
			copy(st, strong[:strongLen])
			idx := len(sig.Blocks)
			sig.Blocks = append(sig.Blocks, Block{Weak: weak, Strong: st})
			sig.weakIndex[weak] = append(sig.weakIndex[weak], idx)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("rdiff: read basis: %w", err)
		}
	}
	return sig, nil
}

// WriteTo serialises the signature in librsync's format:
//
//	magic:uint32  block_len:uint32  strong_len:uint32   (all big-endian)
//	then per block: weak:uint32  strong[strong_len]
func (s *Signature) WriteTo(w io.Writer) (int64, error) {
	var hdr [12]byte
	binary.BigEndian.PutUint32(hdr[0:4], s.Magic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(s.BlockLen))   //nolint:gosec
	binary.BigEndian.PutUint32(hdr[8:12], uint32(s.StrongLen)) //nolint:gosec
	var total int64
	n, err := w.Write(hdr[:])
	total += int64(n)
	if err != nil {
		return total, err
	}
	var ws [4]byte
	for _, b := range s.Blocks {
		binary.BigEndian.PutUint32(ws[:], b.Weak)
		n, err = w.Write(ws[:])
		total += int64(n)
		if err != nil {
			return total, err
		}
		if len(b.Strong) != s.StrongLen {
			return total, fmt.Errorf("rdiff: strong sum length %d != header %d", len(b.Strong), s.StrongLen)
		}
		n, err = w.Write(b.Strong)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ReadSignature parses a librsync signature stream.
func ReadSignature(r io.Reader) (*Signature, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("rdiff: read signature header: %w", err)
	}
	sig := &Signature{
		Magic:     binary.BigEndian.Uint32(hdr[0:4]),
		BlockLen:  int(binary.BigEndian.Uint32(hdr[4:8])),
		StrongLen: int(binary.BigEndian.Uint32(hdr[8:12])),
		weakIndex: make(map[uint32][]int),
	}
	full, err := fullStrongLen(sig.Magic)
	if err != nil {
		return nil, err
	}
	if sig.BlockLen <= 0 {
		return nil, fmt.Errorf("rdiff: bad signature block length %d", sig.BlockLen)
	}
	if sig.StrongLen <= 0 || sig.StrongLen > full {
		return nil, fmt.Errorf("rdiff: bad strong sum length %d (max %d)", sig.StrongLen, full)
	}

	rec := make([]byte, 4+sig.StrongLen)
	for i := 0; ; i++ {
		_, err := io.ReadFull(r, rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("rdiff: read signature block %d: %w", i, err)
		}
		weak := binary.BigEndian.Uint32(rec[0:4])
		strong := make([]byte, sig.StrongLen)
		copy(strong, rec[4:])
		sig.Blocks = append(sig.Blocks, Block{Weak: weak, Strong: strong})
		sig.weakIndex[weak] = append(sig.weakIndex[weak], i)
	}
	return sig, nil
}
