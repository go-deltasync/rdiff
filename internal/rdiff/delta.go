package rdiff

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// librsync delta command opcodes (the leading byte of each command).
const (
	opEnd byte = 0x00

	// 0x01..0x40: LITERAL with the length encoded *in* the opcode (1..64).

	opLiteralN1 byte = 0x41 // literal, length in next 1 byte
	opLiteralN8 byte = 0x44 // literal, length in next 8 bytes (N2/N4 in between)

	opCopyBase byte = 0x45 // COPY commands occupy 0x45..0x54 (16 variants)
)

// intWidth returns the smallest of {1,2,4,8} bytes that can hold v.
func intWidth(v uint64) int {
	switch {
	case v <= 0xff:
		return 1
	case v <= 0xffff:
		return 2
	case v <= 0xffffffff:
		return 4
	default:
		return 8
	}
}

// widthIndex maps a byte width {1,2,4,8} to the librsync param index {0,1,2,3}.
func widthIndex(w int) byte {
	switch w {
	case 1:
		return 0
	case 2:
		return 1
	case 4:
		return 2
	default:
		return 3
	}
}

// appendInt appends v as a big-endian integer using exactly width bytes.
func appendInt(b []byte, v uint64, width int) []byte {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	return append(b, tmp[8-width:]...)
}

func emitLiteral(w io.Writer, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var hdr []byte
	if n := len(data); n <= 64 {
		hdr = []byte{byte(n)} // opcode 0x01..0x40 carries the length itself
	} else {
		width := intWidth(uint64(n))
		hdr = append([]byte{opLiteralN1 + widthIndex(width)}, appendInt(nil, uint64(n), width)...)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func emitCopy(w io.Writer, pos, length uint64) error {
	pw := intWidth(pos)
	lw := intWidth(length)
	op := opCopyBase + widthIndex(pw)*4 + widthIndex(lw)
	buf := []byte{op}
	buf = appendInt(buf, pos, pw)
	buf = appendInt(buf, length, lw)
	_, err := w.Write(buf)
	return err
}

func emitEnd(w io.Writer) error {
	_, err := w.Write([]byte{opEnd})
	return err
}

// GenerateDelta reads all of next and writes a librsync delta stream to out
// that, applied to the basis described by sig, reproduces next.
//
// It runs the rsync algorithm: a rolling weak checksum slides byte-by-byte over
// next; on a weak-sum hit the strong sum confirms a real block match, which is
// emitted as a COPY referencing the basis. Unmatched runs become LITERALs.
//
// Full (BlockLen-sized) basis blocks are matched anywhere in next via the
// rolling scan. The basis's short final block (if any) is additionally matched
// when next ends with those same bytes. A short block appearing mid-stream is
// not matched (emitted literally); the result is always correct, just not
// always maximally compact.
func GenerateDelta(sig *Signature, next io.Reader, out io.Writer) error {
	if sig.BlockLen <= 0 {
		return fmt.Errorf("rdiff: invalid signature block length %d", sig.BlockLen)
	}
	data, err := io.ReadAll(next)
	if err != nil {
		return fmt.Errorf("rdiff: read target: %w", err)
	}

	var magic [4]byte
	binary.BigEndian.PutUint32(magic[:], DeltaMagic)
	if _, err := out.Write(magic[:]); err != nil {
		return err
	}

	blockLen := sig.BlockLen
	n := len(data)
	litStart := 0 // start of the pending unmatched (literal) run
	i := 0
	var rs Rollsum

	for i+blockLen <= n {
		if rs.Count() == 0 {
			rs.Update(data[i : i+blockLen])
		}
		matched := -1
		if cands, ok := sig.weakIndex[rs.Digest()]; ok {
			strong, serr := strongSumFor(sig.Magic, data[i:i+blockLen])
			if serr != nil {
				return serr
			}
			strong = strong[:sig.StrongLen]
			for _, k := range cands {
				if bytes.Equal(sig.Blocks[k].Strong, strong) {
					matched = k
					break
				}
			}
		}

		if matched >= 0 {
			if i > litStart {
				if err := emitLiteral(out, data[litStart:i]); err != nil {
					return err
				}
			}
			if err := emitCopy(out, uint64(matched)*uint64(blockLen), uint64(blockLen)); err != nil {
				return err
			}
			i += blockLen
			litStart = i
			rs.Reset()
			continue
		}

		// No match: slide the window forward by one byte.
		if i+blockLen < n {
			rs.Rotate(data[i], data[i+blockLen])
		}
		i++
	}

	// The basis may end on a short final block (when its length is not a
	// multiple of BlockLen). That block is the only entry whose effective
	// length is < BlockLen, so it never matches the full-width rolling window
	// above. If the new file *ends* with those same bytes, recover them as a
	// COPY instead of a literal. Only one suffix length can match (the strong
	// sum pins both content and length), so the weak-sum pre-check keeps this
	// cheap.
	if litStart < n && len(sig.Blocks) > 0 {
		last := len(sig.Blocks) - 1
		lb := sig.Blocks[last]
		maxL := n - litStart
		if maxL > blockLen {
			maxL = blockLen
		}
		for l := 1; l <= maxL; l++ {
			region := data[n-l : n]
			if WeakSum(region) != lb.Weak {
				continue
			}
			strong, serr := strongSumFor(sig.Magic, region)
			if serr != nil {
				return serr
			}
			if !bytes.Equal(strong[:sig.StrongLen], lb.Strong) {
				continue
			}
			if n-l > litStart {
				if err := emitLiteral(out, data[litStart:n-l]); err != nil {
					return err
				}
			}
			if err := emitCopy(out, uint64(last)*uint64(blockLen), uint64(l)); err != nil {
				return err
			}
			litStart = n
			break
		}
	}

	if litStart < n {
		if err := emitLiteral(out, data[litStart:n]); err != nil {
			return err
		}
	}
	return emitEnd(out)
}
