package prores

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"math/bits"
	"sync"

	"github.com/pkg/errors"
)

const (
	BlockHeight            = 8
	BlockWidth             = 8
	BlocksPerMacroblock    = 4
	MaxMacroblocksPerSlice = 8
	MaxBlocksPerSlice      = MaxMacroblocksPerSlice * BlocksPerMacroblock
)

type SliceHeader struct {
	HeaderSize        int64
	QuantizationIndex int
	LumaDataSize      int
	ChromaUDataSize   int
}

func (h *SliceHeader) Decode(r io.ReaderAt) error {
	var hdrSizeBuf [1]byte
	if _, err := r.ReadAt(hdrSizeBuf[:], 0); err != nil {
		return err
	}

	if hdrSizeBuf[0] < 48 {
		return fmt.Errorf("slice header size must be at least 48")
	} else if hdrSizeBuf[0]%8 != 0 {
		return fmt.Errorf("slice header size not divisible by 8")
	}

	hdrSize := hdrSizeBuf[0] / 8

	buf := make([]byte, hdrSize)
	if _, err := r.ReadAt(buf, 0); err != nil {
		return err
	}

	decoded := SliceHeader{
		HeaderSize:        int64(hdrSize),
		QuantizationIndex: int(buf[1]),
		LumaDataSize:      int(binary.BigEndian.Uint16(buf[2:])),
		ChromaUDataSize:   int(binary.BigEndian.Uint16(buf[4:])),
	}

	*h = decoded
	return nil
}

type CodeParameters byte

func (p CodeParameters) LastRiceQ() int {
	return int(p & 3)
}

func (p CodeParameters) RiceOrder() int {
	return int(p >> 5)
}

func (p CodeParameters) ExpOrder() int {
	return int(p>>2) & 7
}

type codeParametersTableEntry struct {
	ExpGolombSubexpr int
}

var expGolombSubexprs [256]int

func init() {
	for i := range expGolombSubexprs {
		p := CodeParameters(i)
		expGolombSubexprs[i] = -(1 << uint(p.ExpOrder())) + ((p.LastRiceQ() + 1) << uint(p.RiceOrder()))
	}
}

func (p CodeParameters) Decode(bs *Bitstream, dest *int) bool {
	var q int
	if !bs.ReadSmallUnary(&q) {
		return false
	} else if lastRiceQ := p.LastRiceQ(); q > lastRiceQ {
		// exponential-golomb
		k := p.ExpOrder()
		bits := k - lastRiceQ + (q << 1) - q - 1
		var n int
		ret := bs.ReadInt(bits, &n)
		*dest = ((1 << uint(bits)) | n) + expGolombSubexprs[p]
		return ret
	} else if k := p.RiceOrder(); k != 0 {
		// golomb-rice
		var r int
		ret := bs.ReadInt(k, &r)
		*dest = q<<uint(k) + r
		return ret
	} else {
		*dest = q
	}
	return true
}

var dcCodeParams = [7]CodeParameters{0x04, 0x28, 0x28, 0x4D, 0x4D, 0x70, 0x70}

func decodeDCCoefficients(bs *Bitstream, dest *[MaxBlocksPerSlice][64]int16, numberOfBlocks int) error {
	var code int
	if !CodeParameters(0xb8).Decode(bs, &code) {
		return fmt.Errorf("unable to decode initial codeword")
	}
	prev := (((code) >> 1) ^ (-((code) & 1)))
	dest[0][0] = int16(prev)

	code = 5
	sign := 0
	for i := 1; i < numberOfBlocks; i++ {
		params := dcCodeParams[len(dcCodeParams)-1]
		if code < len(dcCodeParams) {
			params = dcCodeParams[code]
		}
		if !params.Decode(bs, &code) {
			return fmt.Errorf("unable to decode codeword")
		}
		if code != 0 {
			sign ^= -(code & 1)
		} else {
			sign = 0
		}
		prev += (((code + 1) >> 1) ^ sign) - sign
		dest[i][0] = int16(prev)
	}

	return nil
}

var acRunCodeParams = [16]CodeParameters{0x06, 0x06, 0x05, 0x05, 0x04, 0x29, 0x29, 0x29, 0x29, 0x28, 0x28, 0x28, 0x28, 0x28, 0x28, 0x4c}
var acLevelCodeParams = [10]CodeParameters{0x04, 0x0a, 0x05, 0x06, 0x04, 0x28, 0x28, 0x28, 0x28, 0x4c}

func decodeACCoefficients(bs *Bitstream, dest *[MaxBlocksPerSlice][64]int16, numberOfBlocks int, scanOrder []int) error {
	run := 4
	level := 2

	log2BlockCount := 31 - uint(bits.LeadingZeros32(uint32(numberOfBlocks)))
	blockMask := numberOfBlocks - 1
	pos := numberOfBlocks - 1

	for !bs.EndOfData() {
		params := acRunCodeParams[len(acRunCodeParams)-1]
		if run < len(acRunCodeParams) {
			params = acRunCodeParams[run]
		}
		if !params.Decode(bs, &run) {
			return fmt.Errorf("unable to decode run bits")
		}
		pos += run + 1

		params = acLevelCodeParams[len(acLevelCodeParams)-1]
		if level < len(acLevelCodeParams) {
			params = acLevelCodeParams[level]
		}
		if !params.Decode(bs, &level) {
			return fmt.Errorf("unable to decode level bits")
		}
		level += 1

		block := pos & blockMask
		if block >= numberOfBlocks {
			return fmt.Errorf("invalid coefficient position")
		}
		i := scanOrder[pos>>log2BlockCount]

		var sign bool
		if !bs.ReadBit(&sign) {
			return fmt.Errorf("unable to decode sign")
		} else if sign {
			dest[block][i] = int16(-level)
		} else {
			dest[block][i] = int16(level)
		}
	}

	return nil
}

func (d *SliceDecoder) decodeCoefficients(coeffs *[MaxBlocksPerSlice][64]int16, b []byte, numberOfBlocks int, scanOrder []int) error {
	bs := &Bitstream{
		Bytes: b,
	}
	if err := decodeDCCoefficients(bs, coeffs, numberOfBlocks); err != nil {
		return errors.Wrap(err, "unable to decode dc coefficients")
	}
	if err := decodeACCoefficients(bs, coeffs, numberOfBlocks, scanOrder); err != nil {
		return errors.Wrap(err, "unable to decode ac coefficients")
	}
	return nil
}

func clamp10bit(n int32) uint16 {
	if uint32(n)&0xfffffc00 != 0 {
		return uint16((-n)>>31) & 0x3ff
	}
	return uint16(n)
}

func decodeBlock(dest []uint8, lineStride int, quantized [64]int16, mat [64]int32) {
	var dequantized block
	dequantized[0] = 4096 + ((int32(quantized[0]) * mat[0]) >> 2)
	for i := 1; i < 64; i++ {
		dequantized[i] = (int32(quantized[i]) * mat[i]) >> 2
	}
	idct(&dequantized)
	for row := 0; row < 8; row++ {
		dequantized := dequantized[row<<3:]
		dest := dest[row*lineStride:]
		_ = dest[7]
		_ = dequantized[7]
		dest[0] = uint8(clamp10bit(dequantized[0]) >> 2)
		dest[1] = uint8(clamp10bit(dequantized[1]) >> 2)
		dest[2] = uint8(clamp10bit(dequantized[2]) >> 2)
		dest[3] = uint8(clamp10bit(dequantized[3]) >> 2)
		dest[4] = uint8(clamp10bit(dequantized[4]) >> 2)
		dest[5] = uint8(clamp10bit(dequantized[5]) >> 2)
		dest[6] = uint8(clamp10bit(dequantized[6]) >> 2)
		dest[7] = uint8(clamp10bit(dequantized[7]) >> 2)
	}
}

func (d *SliceDecoder) decodeChannel(data []byte, dest []uint8, offset func(int, int) int, stride int, rect image.Rectangle, scanOrder []int, scaledMatrix [64]int32, isSubsampled, isChroma bool) error {
	blocksPerSlice := 4 * rect.Dx() / MacroblockWidth
	if isSubsampled {
		blocksPerSlice >>= 1
	}
	if blocksPerSlice > MaxBlocksPerSlice {
		return fmt.Errorf("unsupported slice size")
	}

	coefficients := d.coefficientBuffers.Get().(*[MaxBlocksPerSlice][64]int16)
	defer d.coefficientBuffers.Put(coefficients)

	// XXX: The syntax used here is very specific: This pattern is recognized by the compiler and
	// replaced with a memclr: https://github.com/golang/go/issues/5373
	coeffsSlice := coefficients[:blocksPerSlice]
	for i := range coeffsSlice {
		coeffsSlice[i] = [64]int16{}
	}

	if err := d.decodeCoefficients(coefficients, data, blocksPerSlice, scanOrder); err != nil {
		return err
	}

	if isChroma {
		if isSubsampled {
			for i := 0; i < rect.Dx()/MacroblockWidth; i++ {
				coefficients := coefficients[i*2:]
				decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth, rect.Min.Y):], stride, coefficients[0], scaledMatrix)
				decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth, rect.Min.Y+BlockHeight):], stride, coefficients[1], scaledMatrix)
			}
		} else {
			for i := 0; i < rect.Dx()/MacroblockWidth; i++ {
				coefficients := coefficients[i*4:]
				decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth, rect.Min.Y):], stride, coefficients[0], scaledMatrix)
				decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth, rect.Min.Y+BlockHeight):], stride, coefficients[1], scaledMatrix)
				decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth+BlockWidth, rect.Min.Y):], stride, coefficients[2], scaledMatrix)
				decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth+BlockWidth, rect.Min.Y+BlockHeight):], stride, coefficients[3], scaledMatrix)
			}
		}
	} else {
		for i := 0; i < rect.Dx()/MacroblockWidth; i++ {
			coefficients := coefficients[i*4:]
			decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth, rect.Min.Y):], stride, coefficients[0], scaledMatrix)
			decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth+BlockWidth, rect.Min.Y):], stride, coefficients[1], scaledMatrix)
			decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth, rect.Min.Y+BlockHeight):], stride, coefficients[2], scaledMatrix)
			decodeBlock(dest[offset(rect.Min.X+i*MacroblockWidth+BlockWidth, rect.Min.Y+BlockHeight):], stride, coefficients[3], scaledMatrix)
		}
	}
	return nil
}

// A SliceDecoder facilitates sharing of resources such as memory allocations between slices.
type SliceDecoder struct {
	coefficientBuffers sync.Pool
}

func NewSliceDecoder() *SliceDecoder {
	return &SliceDecoder{
		coefficientBuffers: sync.Pool{
			New: func() interface{} {
				var ret [MaxBlocksPerSlice][64]int16
				return &ret
			},
		},
	}
}

func (d *SliceDecoder) DecodeSlice(r *io.SectionReader, frameHeader *FrameHeader, img *image.YCbCr, rect image.Rectangle, scanOrder []int) error {
	var header SliceHeader
	if err := header.Decode(r); err != nil {
		return err
	}

	pixelData := make([]byte, r.Size()-header.HeaderSize)
	if n, err := r.ReadAt(pixelData, header.HeaderSize); n < len(pixelData) {
		return err
	}

	qScale := int32(header.QuantizationIndex)
	if header.QuantizationIndex >= 129 {
		qScale = 128 + 4*int32(header.QuantizationIndex-128)
	}

	var scaledLumaMatrix [64]int32
	lumaMatrix := frameHeader.LumaQuantizationMatrix()
	for i := 0; i < 64; i++ {
		scaledLumaMatrix[i] = int32(lumaMatrix[i]) * qScale
	}

	lumaData := pixelData[:header.LumaDataSize]
	if err := d.decodeChannel(lumaData, img.Y, img.YOffset, img.YStride, rect, scanOrder, scaledLumaMatrix, false, false); err != nil {
		return errors.Wrap(err, "unable to decode luma channel")
	}
	pixelData = pixelData[header.LumaDataSize:]

	var scaledChromaMatrix [64]int32
	chromaMatrix := frameHeader.ChromaQuantizationMatrix()
	for i := 0; i < 64; i++ {
		scaledChromaMatrix[i] = int32(chromaMatrix[i]) * qScale
	}

	isChromaSubsampled := frameHeader.Flags.SubsampleRatio() == image.YCbCrSubsampleRatio422

	chromaUData := pixelData[:header.ChromaUDataSize]
	if err := d.decodeChannel(chromaUData, img.Cb, img.COffset, img.CStride, rect, scanOrder, scaledChromaMatrix, isChromaSubsampled, true); err != nil {
		return errors.Wrap(err, "unable to decode chroma u channel")
	}
	pixelData = pixelData[header.ChromaUDataSize:]

	chromaVData := pixelData
	if err := d.decodeChannel(chromaVData, img.Cr, img.COffset, img.CStride, rect, scanOrder, scaledChromaMatrix, isChromaSubsampled, true); err != nil {
		return errors.Wrap(err, "unable to decode chroma v channel")
	}

	return nil
}
