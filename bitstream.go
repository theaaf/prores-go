// +build amd64

package prores

import (
	"math/bits"
	"unsafe"
)

type Bitstream struct {
	Offset int
	Bytes  []byte
}

func (bs *Bitstream) RemainingBits() int {
	return len(bs.Bytes)<<3 - bs.Offset
}

// Reads a small unary number (less than 56).
func (bs *Bitstream) ReadSmallUnary(dest *int) bool {
	pos := bs.Offset >> 3
	if l := len(bs.Bytes) - pos; l == 0 {
		return false
	} else if l >= 8 {
		// XXX: This is not portable.
		*dest = bits.LeadingZeros64(bits.ReverseBytes64(*(*uint64)(unsafe.Pointer(&bs.Bytes[pos]))) << uint(bs.Offset&7))
		bs.Offset += *dest + 1
		return true
	}

	buf := bs.Bytes[bs.Offset>>3:]

	skipped := bs.Offset & 7
	mask := byte(0xff >> uint(skipped))
	if masked := buf[0] & mask; masked != 0 {
		*dest = bits.LeadingZeros8(masked << uint(skipped))
		bs.Offset += *dest + 1
		return true
	}

	for i := 1; i < len(buf); i++ {
		if buf[i] != 0 {
			*dest = bits.LeadingZeros8(buf[i]) - skipped + i<<3
			bs.Offset += *dest + 1
			return true
		}
	}
	return false
}

func (bs *Bitstream) ReadBit(dest *bool) bool {
	if bs.RemainingBits() < 1 {
		return false
	}
	*dest = (bs.Bytes[bs.Offset>>3] & byte(1<<uint(7-bs.Offset&7))) != 0
	bs.Offset++
	return true
}

func (bs *Bitstream) ReadInt(bits int, dest *int) bool {
	remaining := bs.RemainingBits()
	switch {
	case remaining < bits || bits > 32:
		return false
	case bits == 0:
		*dest = 0
		return true
	default:
		buf := bs.Bytes[bs.Offset>>3:]
		bitOffset := bs.Offset & 7
		endBit := bitOffset + bits
		shiftRight := uint(8-endBit&7) & 7
		switch {
		case endBit > 24:
			_ = buf[3]
			b0 := buf[0] & byte(0xff>>uint(bitOffset))
			*dest = ((int(b0) << 24) | (int(buf[1]) << 16) | (int(buf[2]) << 8) | int(buf[3])) >> shiftRight
		case endBit > 16:
			_ = buf[2]
			b0 := buf[0] & byte(0xff>>uint(bitOffset))
			*dest = ((int(b0) << 16) | (int(buf[1]) << 8) | int(buf[2])) >> shiftRight
		case endBit > 8:
			_ = buf[1]
			b0 := buf[0] & byte(0xff>>uint(bitOffset))
			*dest = ((int(b0) << 8) | int(buf[1])) >> shiftRight
		default:
			b0 := buf[0] & byte(0xff>>uint(bitOffset))
			*dest = int(b0) >> shiftRight
		}
		bs.Offset += bits
		return true
	}
}

func (bs *Bitstream) EndOfData() bool {
	remaining := bs.RemainingBits()
	switch {
	case remaining >= 32:
		return false
	case remaining <= 0:
		return true
	default:
		buf := bs.Bytes[bs.Offset>>3:]
		switch {
		case remaining > 24:
			_ = buf[3]
			b0 := buf[0] << uint(bs.Offset&7)
			return b0|buf[1]|buf[2]|buf[3] == 0
		case remaining > 16:
			_ = buf[2]
			b0 := buf[0] << uint(bs.Offset&7)
			return b0|buf[1]|buf[2] == 0
		case remaining > 8:
			_ = buf[1]
			b0 := buf[0] << uint(bs.Offset&7)
			return b0|buf[1] == 0
		default:
			b0 := buf[0] << uint(bs.Offset&7)
			return b0 == 0
		}
	}
}
