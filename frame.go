package prores

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
)

type FrameFlags byte

func (f FrameFlags) SubsampleRatio() image.YCbCrSubsampleRatio {
	if (f & 0xc0) == 0xc0 {
		return image.YCbCrSubsampleRatio444
	}
	return image.YCbCrSubsampleRatio422
}

type InterlaceMode int

const (
	InterlaceModeNone      InterlaceMode = 0
	InterlaceModeTopFirst  InterlaceMode = 1
	InterlaceModeTopSecond InterlaceMode = 2
)

func (f FrameFlags) InterlaceMode() InterlaceMode {
	return InterlaceMode((f & 0x0c) >> 2)
}

type FrameAlphaInfo byte

func (i FrameAlphaInfo) HasAlpha() bool {
	return i != 0
}

type FrameQuantizationMatrixFlags byte

func (f FrameQuantizationMatrixFlags) CustomLumaQuantizationMatrixPresent() bool {
	return (f & 2) != 0
}

func (f FrameQuantizationMatrixFlags) CustomChromaQuantizationMatrixPresent() bool {
	return (f & 1) != 0
}

var defaultQuantizationMatrix = []int8{
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
}

type FrameHeader struct {
	HeaderSize                     int64
	Version                        int
	Width                          int
	Height                         int
	Flags                          FrameFlags
	AlphaInfo                      FrameAlphaInfo
	QuantizationMatrixFlags        FrameQuantizationMatrixFlags
	CustomLumaQuantizationMatrix   []int8
	CustomChromaQuantizationMatrix []int8
}

func (h *FrameHeader) LumaQuantizationMatrix() []int8 {
	if h.CustomLumaQuantizationMatrix != nil {
		return h.CustomLumaQuantizationMatrix
	}
	return defaultQuantizationMatrix
}

func (h *FrameHeader) ChromaQuantizationMatrix() []int8 {
	if h.CustomChromaQuantizationMatrix != nil {
		return h.CustomChromaQuantizationMatrix
	}
	return h.LumaQuantizationMatrix()
}

func (h *FrameHeader) Decode(r io.ReaderAt) error {
	var hdrSizeBuf [2]byte
	if _, err := r.ReadAt(hdrSizeBuf[:], 0); err != nil {
		return err
	}

	hdrSize := binary.BigEndian.Uint16(hdrSizeBuf[:])
	if hdrSize < 28 {
		return fmt.Errorf("header size must be at least 28")
	} else if hdrSize > 1024 {
		// to keep us from choking on bad data. not dictated by spec
		return fmt.Errorf("header size must be less than or equal to 1024")
	}

	buf := make([]byte, hdrSize)
	if _, err := r.ReadAt(buf, 0); err != nil {
		return err
	}

	decoded := FrameHeader{
		HeaderSize:              int64(hdrSize),
		Version:                 int(binary.BigEndian.Uint16(buf[2:])),
		Width:                   int(binary.BigEndian.Uint16(buf[8:])),
		Height:                  int(binary.BigEndian.Uint16(buf[10:])),
		Flags:                   FrameFlags(buf[12]),
		AlphaInfo:               FrameAlphaInfo(buf[17] & 0x0f),
		QuantizationMatrixFlags: FrameQuantizationMatrixFlags(buf[19]),
	}

	customMatrixOffset := 20
	if decoded.QuantizationMatrixFlags.CustomLumaQuantizationMatrixPresent() {
		m := make([]int8, 64)
		for i := range m {
			m[i] = int8(buf[customMatrixOffset+i])
		}
		decoded.CustomLumaQuantizationMatrix = m
		customMatrixOffset += 64
	}
	if decoded.QuantizationMatrixFlags.CustomChromaQuantizationMatrixPresent() {
		m := make([]int8, 64)
		for i := range m {
			m[i] = int8(buf[customMatrixOffset+i])
		}
		decoded.CustomChromaQuantizationMatrix = m
	}

	*h = decoded
	return nil
}

func DecodeFrame(r io.ReaderAt, size int64) (image.Image, error) {
	var header FrameHeader
	if err := header.Decode(r); err != nil {
		return nil, err
	}

	return DecodePicture(io.NewSectionReader(r, header.HeaderSize, size-header.HeaderSize), &header, FieldOrderFirst)
}
