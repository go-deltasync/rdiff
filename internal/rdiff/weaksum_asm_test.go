package rdiff

import (
	"math/rand"
	"testing"
)

// TestWeakSumASM checks the assembly weak checksum matches the pure-Go WeakSum
// bit-for-bit, including the uint16 wraparound, across edge-case lengths.
func TestWeakSumASM(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{0, 1, 2, 7, 8, 15, 16, 17, 31, 255, 256, 257, 1024, 2048, 4096} {
		b := make([]byte, n)
		rng.Read(b)
		if got, want := weakSumASM(b), WeakSum(b); got != want {
			t.Fatalf("n=%d: weakSumASM=%#08x want %#08x", n, got, want)
		}
	}
	// All-0xFF stresses the 16-bit overflow of both s1 and s2.
	for _, n := range []int{256, 2048} {
		b := make([]byte, n)
		for i := range b {
			b[i] = 0xFF
		}
		if got, want := weakSumASM(b), WeakSum(b); got != want {
			t.Fatalf("0xFF n=%d: weakSumASM=%#08x want %#08x", n, got, want)
		}
	}
}

func benchWeakSum(b *testing.B, f func([]byte) uint32) {
	buf := make([]byte, 2048) // a typical signature block size
	rand.New(rand.NewSource(2)).Read(buf)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f(buf)
	}
}

func BenchmarkWeakSumGo(b *testing.B)  { benchWeakSum(b, WeakSum) }
func BenchmarkWeakSumASM(b *testing.B) { benchWeakSum(b, weakSumASM) }
