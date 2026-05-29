// rdiff is a pure-Go, librsync-interoperable implementation of the rsync
// algorithm's offline signature/delta/patch workflow. It mirrors the interface
// of Martin Pool / librsync's `rdiff`:
//
//	rdiff signature BASIS    SIGNATURE
//	rdiff delta     SIGNATURE NEWFILE  DELTA
//	rdiff patch     BASIS    DELTA     NEWFILE
//
// A single dash ("-") may be used for the streamable input/output of each
// command (patch's BASIS must be a real file because it is read at random
// offsets).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/go-deltasync/rdiff/internal/rdiff"
	"github.com/spf13/cobra"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "rdiff",
		Short:         "Pure-Go, librsync-compatible signature/delta/patch",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(signatureCmd(), deltaCmd(), patchCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "rdiff: %v\n", err)
		os.Exit(1)
	}
}

func signatureCmd() *cobra.Command {
	var (
		blockLen  int
		strongLen int
		hash      string
	)
	cmd := &cobra.Command{
		Use:   "signature [flags] BASIS SIGNATURE",
		Short: "Compute the signature of BASIS",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			magic, err := magicForHash(hash)
			if err != nil {
				return err
			}
			in, closeIn, err := openInput(args[0])
			if err != nil {
				return err
			}
			defer closeIn()

			sig, err := rdiff.GenerateSignature(in, blockLen, strongLen, magic)
			if err != nil {
				return err
			}
			out, closeOut, err := createOutput(args[1])
			if err != nil {
				return err
			}
			defer closeOut()
			_, err = sig.WriteTo(out)
			return err
		},
	}
	cmd.Flags().IntVarP(&blockLen, "block-size", "b", rdiff.DefaultBlockLen, "signature block size in bytes")
	cmd.Flags().IntVarP(&strongLen, "strong-size", "S", 0, "truncate strong sum to this many bytes (0 = full digest)")
	cmd.Flags().StringVarP(&hash, "hash", "H", "blake2", "strong-sum hash: blake2 or md4")
	return cmd
}

func deltaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delta SIGNATURE NEWFILE DELTA",
		Short: "Compute the delta from SIGNATURE's basis to NEWFILE",
		Args:  cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			sigIn, closeSig, err := openInput(args[0])
			if err != nil {
				return err
			}
			defer closeSig()
			sig, err := rdiff.ReadSignature(sigIn)
			if err != nil {
				return err
			}

			next, closeNext, err := openInput(args[1])
			if err != nil {
				return err
			}
			defer closeNext()

			out, closeOut, err := createOutput(args[2])
			if err != nil {
				return err
			}
			defer closeOut()
			return rdiff.GenerateDelta(sig, next, out)
		},
	}
}

func patchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "patch BASIS DELTA NEWFILE",
		Short: "Apply DELTA to BASIS, producing NEWFILE",
		Args:  cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			if args[0] == "-" {
				return fmt.Errorf("patch BASIS must be a regular file (it is read at random offsets), not stdin")
			}
			basis, err := os.Open(args[0])
			if err != nil {
				return fmt.Errorf("open basis %s: %w", args[0], err)
			}
			defer basis.Close()

			delta, closeDelta, err := openInput(args[1])
			if err != nil {
				return err
			}
			defer closeDelta()

			out, closeOut, err := createOutput(args[2])
			if err != nil {
				return err
			}
			defer closeOut()
			return rdiff.Patch(basis, delta, out)
		},
	}
}

func magicForHash(hash string) (uint32, error) {
	switch hash {
	case "blake2", "blake2b":
		return rdiff.Blake2SigMagic, nil
	case "md4":
		return rdiff.MD4SigMagic, nil
	default:
		return 0, fmt.Errorf("unknown hash %q (want blake2 or md4)", hash)
	}
}

// openInput returns a reader for path ("-" => stdin) and a close function.
func openInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// createOutput returns a writer for path ("-" => stdout) and a close function.
func createOutput(path string) (io.Writer, func(), error) {
	if path == "-" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}
