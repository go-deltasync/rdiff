package rdiff

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// readInt reads a big-endian unsigned integer of the given byte width.
func readInt(r io.Reader, width int) (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[8-width:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(buf[:]), nil
}

// Patch applies a librsync delta stream to basis and writes the reconstructed
// target to out. basis must support random access (io.ReaderAt) because COPY
// commands reference arbitrary offsets.
func Patch(basis io.ReaderAt, delta io.Reader, out io.Writer) error {
	br := bufio.NewReaderSize(delta, 1<<16)

	var magic [4]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return fmt.Errorf("rdiff: read delta magic: %w", err)
	}
	if got := binary.BigEndian.Uint32(magic[:]); got != DeltaMagic {
		return fmt.Errorf("rdiff: bad delta magic %#08x (want %#08x)", got, DeltaMagic)
	}

	for {
		op, err := br.ReadByte()
		if err != nil {
			return fmt.Errorf("rdiff: truncated delta (no END command): %w", err)
		}

		switch {
		case op == opEnd:
			return nil

		case op >= 0x01 && op <= 0x40: // LITERAL, inline length
			if _, err := io.CopyN(out, br, int64(op)); err != nil {
				return fmt.Errorf("rdiff: short literal: %w", err)
			}

		case op >= opLiteralN1 && op <= opLiteralN8: // LITERAL, length param
			width := 1 << (op - opLiteralN1)
			length, err := readInt(br, width)
			if err != nil {
				return fmt.Errorf("rdiff: read literal length: %w", err)
			}
			if _, err := io.CopyN(out, br, int64(length)); err != nil { //nolint:gosec
				return fmt.Errorf("rdiff: short literal: %w", err)
			}

		case op >= opCopyBase && op <= opCopyBase+15: // COPY
			rel := op - opCopyBase
			posWidth := 1 << (rel / 4)
			lenWidth := 1 << (rel % 4)
			pos, err := readInt(br, posWidth)
			if err != nil {
				return fmt.Errorf("rdiff: read copy offset: %w", err)
			}
			length, err := readInt(br, lenWidth)
			if err != nil {
				return fmt.Errorf("rdiff: read copy length: %w", err)
			}
			sr := io.NewSectionReader(basis, int64(pos), int64(length)) //nolint:gosec
			if _, err := io.CopyN(out, sr, int64(length)); err != nil {  //nolint:gosec
				return fmt.Errorf("rdiff: copy from basis at %d+%d: %w", pos, length, err)
			}

		default:
			return fmt.Errorf("rdiff: unsupported delta opcode %#02x", op)
		}
	}
}
