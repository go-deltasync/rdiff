// Package rdiff implements the librsync wire formats (signature, delta) and
// the rsync algorithm in pure Go, so the `rdiff` CLI in this module is
// interoperable with Martin Pool / librsync's `rdiff`.
//
// References:
//   - librsync: https://github.com/librsync/librsync
//   - Andrew Tridgell, "The rsync algorithm" (1996).
//
// The three operations mirror librsync exactly:
//
//	signature  BASIS    -> SIGNATURE   (weak+strong sums of every block of BASIS)
//	delta      SIGNATURE NEW -> DELTA  (instructions to turn BASIS into NEW)
//	patch      BASIS DELTA  -> NEW     (apply the instructions)
package rdiff

// RollsumCharOffset is librsync's RS_CHAR_OFFSET: a constant added to every
// byte before it enters the weak checksum. It exists so that runs of zero
// bytes still perturb the sum.
const RollsumCharOffset = 31

// Rollsum is librsync's rolling weak checksum (a Fletcher/Adler-style sum).
//
// For a window of bytes b[0..L):
//
//	s1 = ( sum_{i}     (b[i] + OFFSET) ) mod 2^16
//	s2 = ( sum_{i} (L-i)*(b[i] + OFFSET) ) mod 2^16
//
// The 32-bit digest packs them as  (s2 << 16) | s1  (big-endian on the wire).
//
// s1 and s2 are deliberately uint16: librsync relies on 16-bit wraparound, so
// the Go arithmetic below must wrap identically. Do NOT widen these fields.
type Rollsum struct {
	count uint64
	s1    uint16
	s2    uint16
}

// Reset clears the running state.
func (r *Rollsum) Reset() { r.count, r.s1, r.s2 = 0, 0, 0 }

// Update folds an entire block into a fresh sum (Reset first, then add each
// byte). This is what signature generation uses per block.
func (r *Rollsum) Update(block []byte) {
	r.Reset()
	var s1, s2 uint16
	for _, b := range block {
		s1 += uint16(b) + RollsumCharOffset
		s2 += s1
	}
	r.s1, r.s2 = s1, s2
	r.count = uint64(len(block))
}

// Rotate slides the window forward by one byte: `out` leaves the window on the
// left, `in` enters on the right. The window width (count) is unchanged. This
// is the hot path of delta scanning.
func (r *Rollsum) Rotate(out, in byte) {
	r.s1 += uint16(in) - uint16(out)
	r.s2 += r.s1 - uint16(r.count)*(uint16(out)+RollsumCharOffset)
}

// Digest returns the 32-bit weak checksum: (s2 << 16) | s1.
func (r *Rollsum) Digest() uint32 {
	return uint32(r.s2)<<16 | uint32(r.s1)
}

// Count is the current window width in bytes.
func (r *Rollsum) Count() uint64 { return r.count }

// WeakSum is a convenience wrapper computing the weak checksum of a block.
func WeakSum(block []byte) uint32 {
	var r Rollsum
	r.Update(block)
	return r.Digest()
}
