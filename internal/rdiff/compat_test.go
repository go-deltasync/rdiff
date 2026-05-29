//go:build compat

// Cross-implementation interoperability tests against the upstream librsync
// `rdiff` CLI. Run with: go test -tags=compat ./internal/rdiff/...
// The whole file is skipped if `rdiff` is not on PATH.
package rdiff

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireRdiff(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rdiff")
	if err != nil {
		t.Skip("librsync `rdiff` not found on PATH; skipping cross-impl compat")
	}
	return path
}

func runRdiff(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("rdiff", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rdiff %v failed: %v\n%s", args, err, out)
	}
}

// makeBasisAndNext writes a basis and a derived (edited) new file to dir and
// returns their paths and contents.
func makeBasisAndNext(t *testing.T, dir string) (basisPath, nextPath string, basis, next []byte) {
	t.Helper()
	basis = randBytes(100, 50_000)
	next = append([]byte{}, basis[:20_000]...)
	next = append(next, []byte("--cross-impl-inserted-bytes--")...)
	next = append(next, basis[25_000:]...)

	basisPath = filepath.Join(dir, "basis.bin")
	nextPath = filepath.Join(dir, "next.bin")
	if err := os.WriteFile(basisPath, basis, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nextPath, next, 0o644); err != nil {
		t.Fatal(err)
	}
	return
}

// TestGoSigCDeltaGoPatch: Go writes the signature, C computes the delta from
// it, Go applies the delta. Exercises C reading our signature and producing a
// delta our patcher accepts.
func TestGoSigCDeltaGoPatch(t *testing.T) {
	requireRdiff(t)
	dir := t.TempDir()
	basisPath, _, basis, next := makeBasisAndNext(t, dir)

	sig, err := GenerateSignature(bytes.NewReader(basis), DefaultBlockLen, 0, Blake2SigMagic)
	if err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "go.sig")
	sf, err := os.Create(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sig.WriteTo(sf); err != nil {
		t.Fatal(err)
	}
	sf.Close()

	deltaPath := filepath.Join(dir, "c.delta")
	runRdiff(t, "delta", sigPath, filepath.Join(dir, "next.bin"), deltaPath)

	delta, err := os.Open(deltaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer delta.Close()
	basisFile, err := os.Open(basisPath)
	if err != nil {
		t.Fatal(err)
	}
	defer basisFile.Close()

	var out bytes.Buffer
	if err := Patch(basisFile, delta, &out); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !bytes.Equal(out.Bytes(), next) {
		t.Fatal("Go-sig / C-delta / Go-patch reconstruction mismatch")
	}
}

// TestCSigGoDeltaCPatch: C writes the signature, Go computes the delta, C
// applies it. Exercises Go reading C's signature and emitting a delta the C
// patcher accepts.
func TestCSigGoDeltaCPatch(t *testing.T) {
	requireRdiff(t)
	dir := t.TempDir()
	basisPath, nextPath, _, next := makeBasisAndNext(t, dir)

	sigPath := filepath.Join(dir, "c.sig")
	// Force the legacy rollsum + blake2 magic (0x72730137). Modern librsync
	// defaults to rabinkarp + blake2 (0x72730147 = RS_RK_BLAKE2_SIG_MAGIC),
	// which is a wire-format extension we don't yet implement. The compat
	// gate verifies on-the-wire byte compatibility with the OLDER format
	// that's been stable for ~20 years; the RK variant is a Phase 2 item.
	runRdiff(t, "signature", "--rollsum=rollsum", basisPath, sigPath)

	sf, err := os.Open(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ReadSignature(sf)
	sf.Close()
	if err != nil {
		t.Fatalf("ReadSignature (from C): %v", err)
	}

	nextFile, err := os.Open(nextPath)
	if err != nil {
		t.Fatal(err)
	}
	defer nextFile.Close()
	deltaPath := filepath.Join(dir, "go.delta")
	df, err := os.Create(deltaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := GenerateDelta(sig, nextFile, df); err != nil {
		t.Fatal(err)
	}
	df.Close()

	outPath := filepath.Join(dir, "c.out")
	runRdiff(t, "patch", basisPath, deltaPath, outPath)

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, next) {
		t.Fatal("C-sig / Go-delta / C-patch reconstruction mismatch")
	}
}
