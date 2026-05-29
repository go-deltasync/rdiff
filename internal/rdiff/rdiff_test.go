package rdiff

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

// failReader always fails, to exercise read-error paths.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errBoom }

// failWriter succeeds until its failAt-th Write call, then fails.
type failWriter struct {
	failAt int
	calls  int
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.failAt {
		return 0, errBoom
	}
	return len(p), nil
}

// roundTrip runs signature -> delta -> patch and returns the reconstructed
// bytes plus the delta size, failing the test on any error.
func roundTrip(t *testing.T, basis, next []byte, blockLen int, magic uint32) (out []byte, deltaLen int) {
	t.Helper()
	sig, err := GenerateSignature(bytes.NewReader(basis), blockLen, 0, magic)
	if err != nil {
		t.Fatalf("GenerateSignature: %v", err)
	}
	var delta bytes.Buffer
	if err := GenerateDelta(sig, bytes.NewReader(next), &delta); err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	var rebuilt bytes.Buffer
	if err := Patch(bytes.NewReader(basis), bytes.NewReader(delta.Bytes()), &rebuilt); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	return rebuilt.Bytes(), delta.Len()
}

func randBytes(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	_, _ = r.Read(b)
	return b
}

func TestRoundTripEdits(t *testing.T) {
	magics := map[string]uint32{"blake2": Blake2SigMagic, "md4": MD4SigMagic}
	basis := randBytes(1, 40_000)

	// next = basis with a deletion, a modification and an insertion.
	next := append([]byte{}, basis[:10_000]...)
	next = append(next, []byte("INSERTED-CHUNK-OF-NEW-DATA")...)
	next = append(next, basis[15_000:25_000]...) // skip 10k (deletion)
	mod := append([]byte{}, basis[25_000:]...)
	for i := 0; i < 50; i++ {
		mod[i*7%len(mod)] ^= 0xff // scattered modifications
	}
	next = append(next, mod...)

	for name, magic := range magics {
		t.Run(name, func(t *testing.T) {
			out, dlen := roundTrip(t, basis, next, 1024, magic)
			if !bytes.Equal(out, next) {
				t.Fatalf("reconstruction mismatch: got %d bytes, want %d", len(out), len(next))
			}
			t.Logf("delta=%d bytes for next=%d bytes", dlen, len(next))
		})
	}
}

func TestDeltaReusesBlocks(t *testing.T) {
	basis := randBytes(2, 200_000)
	next := append([]byte{}, basis...)
	// Flip a handful of bytes in the middle; the vast majority of blocks
	// should still be COPY-able.
	for i := 100_000; i < 100_010; i++ {
		next[i] ^= 0xaa
	}
	out, dlen := roundTrip(t, basis, next, 1024, Blake2SigMagic)
	if !bytes.Equal(out, next) {
		t.Fatal("reconstruction mismatch")
	}
	// With near-total reuse the delta must be a small fraction of the file.
	if dlen > len(next)/4 {
		t.Fatalf("delta too large: %d bytes for %d-byte file (expected heavy reuse)", dlen, len(next))
	}
}

func TestShortTailBlockReused(t *testing.T) {
	const blockLen = 1024
	// Basis length deliberately NOT a multiple of blockLen -> short final block.
	basis := randBytes(8, 5*blockLen+317)
	// next keeps the exact same tail but rewrites the first block.
	next := append([]byte{}, basis...)
	for i := 0; i < blockLen; i++ {
		next[i] ^= 0x5a
	}
	out, dlen := roundTrip(t, basis, next, blockLen, Blake2SigMagic)
	if !bytes.Equal(out, next) {
		t.Fatal("reconstruction mismatch")
	}
	// First block is literal (~1024B); everything after — including the 317-byte
	// short tail — should be COPYs, so the delta stays close to one block.
	if dlen > blockLen+256 {
		t.Fatalf("delta too large: %d bytes; short tail block was likely not reused", dlen)
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name          string
		basis, next   []byte
		blockLen      int
	}{
		{"empty-basis-empty-next", nil, nil, 64},
		{"empty-basis-some-next", nil, []byte("hello world"), 64},
		{"some-basis-empty-next", []byte("hello world"), nil, 64},
		{"identical", randBytes(3, 5000), nil, 64}, // next filled below
		{"basis-smaller-than-block", []byte("tiny"), []byte("tiny"), 64},
		{"totally-different", randBytes(4, 3000), randBytes(5, 3000), 128},
	}
	for i := range cases {
		if cases[i].name == "identical" {
			cases[i].next = append([]byte{}, cases[i].basis...)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _ := roundTrip(t, c.basis, c.next, c.blockLen, Blake2SigMagic)
			if !bytes.Equal(out, c.next) {
				t.Fatalf("mismatch: got %q want %q", out, c.next)
			}
		})
	}
}

func TestSignatureSerializationRoundTrip(t *testing.T) {
	basis := randBytes(6, 12_345)
	sig, err := GenerateSignature(bytes.NewReader(basis), 512, 0, Blake2SigMagic)
	if err != nil {
		t.Fatalf("GenerateSignature: %v", err)
	}
	var buf bytes.Buffer
	if _, err := sig.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	got, err := ReadSignature(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadSignature: %v", err)
	}
	if got.Magic != sig.Magic || got.BlockLen != sig.BlockLen || got.StrongLen != sig.StrongLen {
		t.Fatalf("header mismatch: %+v vs %+v", got, sig)
	}
	if len(got.Blocks) != len(sig.Blocks) {
		t.Fatalf("block count mismatch: %d vs %d", len(got.Blocks), len(sig.Blocks))
	}
	for i := range sig.Blocks {
		if got.Blocks[i].Weak != sig.Blocks[i].Weak || !bytes.Equal(got.Blocks[i].Strong, sig.Blocks[i].Strong) {
			t.Fatalf("block %d mismatch", i)
		}
	}
}

func TestLiteralWidths(t *testing.T) {
	// Empty basis => everything is a literal; the size selects the LITERAL
	// opcode width (inline / N1 / N2 / N4), exercising both emit and decode.
	for _, size := range []int{40, 100, 300, 70_000} {
		next := randBytes(int64(size), size)
		out, _ := roundTrip(t, nil, next, 1024, Blake2SigMagic)
		if !bytes.Equal(out, next) {
			t.Fatalf("size %d: literal round-trip mismatch", size)
		}
	}
}

func TestCopyWidthsSmallOffsets(t *testing.T) {
	// Tiny blockLen + small basis => COPY offsets/lengths fit in one byte,
	// exercising the COPY_N1_N1 decode path.
	basis := randBytes(9, 200)
	next := append([]byte{}, basis...)
	out, _ := roundTrip(t, basis, next, 16, Blake2SigMagic)
	if !bytes.Equal(out, next) {
		t.Fatal("small-offset copy round-trip mismatch")
	}
}

func TestUnknownMagicRejected(t *testing.T) {
	if _, err := GenerateSignature(bytes.NewReader([]byte("x")), 16, 0, 0xdeadbeef); err == nil {
		t.Fatal("GenerateSignature accepted unknown magic")
	}
	if _, err := strongSumFor(0xdeadbeef, []byte("x")); err == nil {
		t.Fatal("strongSumFor accepted unknown magic")
	}
	if _, err := fullStrongLen(0xdeadbeef); err == nil {
		t.Fatal("fullStrongLen accepted unknown magic")
	}
}

func sigHeader(magic uint32, blockLen, strongLen int) []byte {
	var h [12]byte
	binary.BigEndian.PutUint32(h[0:4], magic)
	binary.BigEndian.PutUint32(h[4:8], uint32(blockLen))
	binary.BigEndian.PutUint32(h[8:12], uint32(strongLen))
	return h[:]
}

func TestReadSignatureErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"short-header", []byte{0, 1, 2}},
		{"bad-magic", sigHeader(0xdeadbeef, 1024, 16)},
		{"zero-blocklen", sigHeader(Blake2SigMagic, 0, 32)},
		{"zero-stronglen", sigHeader(Blake2SigMagic, 1024, 0)},
		{"stronglen-too-big", sigHeader(Blake2SigMagic, 1024, 99)},
		{"truncated-block", append(sigHeader(Blake2SigMagic, 1024, 32), 0x01, 0x02)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ReadSignature(bytes.NewReader(c.data)); err == nil {
				t.Fatalf("expected error for %s", c.name)
			}
		})
	}
}

func deltaWith(payload ...byte) []byte {
	var m [4]byte
	binary.BigEndian.PutUint32(m[:], DeltaMagic)
	return append(m[:], payload...)
}

func TestPatchErrors(t *testing.T) {
	basis := []byte("hello world")
	cases := []struct {
		name  string
		delta []byte
	}{
		{"bad-magic", []byte{0, 0, 0, 0, opEnd}},
		{"unsupported-opcode", deltaWith(0x55)},
		{"no-end", deltaWith(0x01, 'x')},               // literal but stream ends, no END
		{"short-literal", deltaWith(0x05, 'a', 'b')},   // says 5 bytes, only 2 present
		{"truncated-copy-offset", deltaWith(0x49, 0x00)}, // COPY needs 2-byte offset, 1 given
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := Patch(bytes.NewReader(basis), bytes.NewReader(c.delta), &out); err == nil {
				t.Fatalf("expected error for %s", c.name)
			} else if c.name == "bad-magic" && !strings.Contains(err.Error(), "magic") {
				t.Fatalf("expected magic error, got %v", err)
			}
		})
	}
}

func TestPatchManualLiteralAndCopy(t *testing.T) {
	basis := []byte("ABCDEFGHIJ")
	// LITERAL "xy" (inline op 0x02) then COPY 4 bytes from offset 2 ("CDEF").
	delta := deltaWith(0x02, 'x', 'y', 0x45, 0x02, 0x04, opEnd)
	var out bytes.Buffer
	if err := Patch(bytes.NewReader(basis), bytes.NewReader(delta), &out); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if got := out.String(); got != "xyCDEF" {
		t.Fatalf("got %q, want %q", got, "xyCDEF")
	}
}

func TestIntWidthAndIndex(t *testing.T) {
	for _, c := range []struct {
		v   uint64
		w   int
		idx byte
	}{
		{0, 1, 0},
		{0xff, 1, 0},
		{0x100, 2, 1},
		{0xffff, 2, 1},
		{0x10000, 4, 2},
		{0xffffffff, 4, 2},
		{0x100000000, 8, 3}, // exercises the 8-byte branch
	} {
		if got := intWidth(c.v); got != c.w {
			t.Fatalf("intWidth(%#x)=%d want %d", c.v, got, c.w)
		}
		if got := widthIndex(c.w); got != c.idx {
			t.Fatalf("widthIndex(%d)=%d want %d", c.w, got, c.idx)
		}
	}
}

func TestWriteToErrors(t *testing.T) {
	sig, err := GenerateSignature(bytes.NewReader(randBytes(11, 4000)), 512, 0, Blake2SigMagic)
	if err != nil {
		t.Fatalf("GenerateSignature: %v", err)
	}
	// Header write fails, then weak write, then strong write.
	for _, failAt := range []int{1, 2, 3} {
		if _, err := sig.WriteTo(&failWriter{failAt: failAt}); err == nil {
			t.Fatalf("WriteTo failAt=%d: expected error", failAt)
		}
	}
	// Strong-sum length disagreeing with the header must be rejected.
	bad := &Signature{Magic: Blake2SigMagic, BlockLen: 512, StrongLen: 32,
		Blocks: []Block{{Weak: 1, Strong: make([]byte, 4)}}}
	if _, err := bad.WriteTo(io.Discard); err == nil {
		t.Fatal("WriteTo accepted mismatched strong length")
	}
}

func TestGenerateSignatureReadError(t *testing.T) {
	if _, err := GenerateSignature(failReader{}, 512, 0, Blake2SigMagic); err == nil {
		t.Fatal("expected read error")
	}
}

func TestGenerateDeltaErrors(t *testing.T) {
	sig, err := GenerateSignature(bytes.NewReader(randBytes(12, 4000)), 512, 0, Blake2SigMagic)
	if err != nil {
		t.Fatalf("GenerateSignature: %v", err)
	}
	// Reading the target fails.
	if err := GenerateDelta(sig, failReader{}, io.Discard); err == nil {
		t.Fatal("expected target read error")
	}
	// Invalid signature block length.
	if err := GenerateDelta(&Signature{BlockLen: 0}, bytes.NewReader([]byte("x")), io.Discard); err == nil {
		t.Fatal("expected block-length error")
	}
	// Output write fails at the magic, and later during a literal write.
	next := randBytes(13, 4000)
	for _, failAt := range []int{1, 2, 3} {
		if err := GenerateDelta(sig, bytes.NewReader(next), &failWriter{failAt: failAt}); err == nil {
			t.Fatalf("GenerateDelta failAt=%d: expected write error", failAt)
		}
	}
}

func TestPatchWriteError(t *testing.T) {
	basis := []byte("ABCDEFGHIJ")
	delta := deltaWith(0x02, 'x', 'y', 0x45, 0x02, 0x04, opEnd)
	// Fail while writing the literal (call 1) and while writing the copy.
	for _, failAt := range []int{1, 2} {
		if err := Patch(bytes.NewReader(basis), bytes.NewReader(delta), &failWriter{failAt: failAt}); err == nil {
			t.Fatalf("Patch failAt=%d: expected write error", failAt)
		}
	}
}

func TestRollsumRotateMatchesRecompute(t *testing.T) {
	data := randBytes(7, 4096)
	const w = 64
	var rs Rollsum
	rs.Update(data[0:w])
	for i := 0; i+w < len(data); i++ {
		if got := rs.Digest(); got != WeakSum(data[i:i+w]) {
			t.Fatalf("rolling digest at %d = %#08x, recompute = %#08x", i, got, WeakSum(data[i:i+w]))
		}
		rs.Rotate(data[i], data[i+w])
	}
}
