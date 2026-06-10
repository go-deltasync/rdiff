//go:build !arm64

package rdiff

// weakSumASM falls back to the pure-Go path on architectures without an
// assembly implementation, so the benchmark and test build everywhere.
func weakSumASM(block []byte) uint32 {
	var r Rollsum
	r.Update(block)
	return r.Digest()
}
