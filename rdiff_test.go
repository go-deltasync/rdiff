package rdiff_test

import (
	"bytes"
	"testing"

	"github.com/go-deltasync/rdiff"
)

// TestFacadeRoundTrip drives the public signature → delta → patch workflow.
func TestFacadeRoundTrip(t *testing.T) {
	basis := bytes.Repeat([]byte("the quick brown fox\n"), 500)
	next := append(append([]byte{}, basis[:3000]...), append([]byte("EDITED"), basis[3100:]...)...)

	sig, err := rdiff.GenerateSignature(bytes.NewReader(basis), rdiff.DefaultBlockLen, 0, rdiff.Blake2SigMagic)
	if err != nil {
		t.Fatalf("GenerateSignature: %v", err)
	}
	var delta bytes.Buffer
	if err := rdiff.GenerateDelta(sig, bytes.NewReader(next), &delta); err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	var out bytes.Buffer
	if err := rdiff.Patch(bytes.NewReader(basis), bytes.NewReader(delta.Bytes()), &out); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !bytes.Equal(out.Bytes(), next) {
		t.Fatal("façade round-trip mismatch")
	}
	// ReadSignature parses what GenerateSignature serialised.
	var sigBuf bytes.Buffer
	if _, err := sig.WriteTo(&sigBuf); err != nil {
		t.Fatalf("Signature.WriteTo: %v", err)
	}
	if _, err := rdiff.ReadSignature(bytes.NewReader(sigBuf.Bytes())); err != nil {
		t.Fatalf("ReadSignature: %v", err)
	}
}
