package prores

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"sync"
)

const MacroblockWidth = 16
const MacroblockHeight = 16

type PictureHeader struct {
	HeaderSize        int64
	NumberOfSlices    int
	SliceWidthFactor  int
	SliceHeightFactor int
}

func (h *PictureHeader) SliceWidthMacroblocks() int {
	return 1 << uint(h.SliceWidthFactor)
}

func (h *PictureHeader) SliceHeightMacroblocks() int {
	return 1 << uint(h.SliceHeightFactor)
}

func (h *PictureHeader) Decode(r io.ReaderAt) error {
	var hdrSizeBuf [1]byte
	if _, err := r.ReadAt(hdrSizeBuf[:], 0); err != nil {
		return err
	}

	if hdrSizeBuf[0] < 64 {
		return fmt.Errorf("picture header size must be at least 64")
	} else if hdrSizeBuf[0]%8 != 0 {
		return fmt.Errorf("picture header size not divisible by 8")
	}

	hdrSize := hdrSizeBuf[0] / 8

	buf := make([]byte, hdrSize)
	if _, err := r.ReadAt(buf, 0); err != nil {
		return err
	}

	decoded := PictureHeader{
		HeaderSize:        int64(hdrSize),
		NumberOfSlices:    int(binary.BigEndian.Uint16(buf[5:])),
		SliceWidthFactor:  int(buf[7] >> 4),
		SliceHeightFactor: int(buf[7] & 0x0f),
	}

	*h = decoded
	return nil
}

var ProgressiveScanOrder = []int{
	0, 1, 8, 9, 2, 3, 10, 11,
	16, 17, 24, 25, 18, 19, 26, 27,
	4, 5, 12, 20, 13, 6, 7, 14,
	21, 28, 29, 22, 15, 23, 30, 31,
	32, 33, 40, 48, 41, 34, 35, 42,
	49, 56, 57, 50, 43, 36, 37, 44,
	51, 58, 59, 52, 45, 38, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

var InterlacedScanOrder = []int{
	0, 8, 1, 9, 16, 24, 17, 25,
	2, 10, 3, 11, 18, 26, 19, 27,
	32, 40, 33, 34, 41, 48, 56, 49,
	42, 35, 43, 50, 57, 58, 51, 59,
	4, 12, 5, 6, 13, 20, 28, 21,
	14, 7, 15, 22, 29, 36, 44, 37,
	30, 23, 31, 38, 45, 52, 60, 53,
	46, 39, 47, 54, 61, 62, 55, 63,
}

type decodeSliceJob struct {
	offset  int64
	x       int
	y       int
	width   int
	dataLen int64
}

type FieldOrder int

const (
	FieldOrderFirst  FieldOrder = 1
	FieldOrderSecond FieldOrder = 2
)

func DecodePicture(r io.ReaderAt, frameHeader *FrameHeader, fieldOrder FieldOrder) (image.Image, error) {
	if frameHeader.AlphaInfo.HasAlpha() {
		return nil, fmt.Errorf("alpha channels not supported")
	}

	scanOrder := ProgressiveScanOrder
	height := frameHeader.Height

	switch frameHeader.Flags.InterlaceMode() {
	case InterlaceModeTopFirst:
		scanOrder = InterlacedScanOrder
		if fieldOrder == FieldOrderFirst {
			height = (height + 1) / 2
		} else {
			height = height / 2
		}
	case InterlaceModeTopSecond:
		scanOrder = InterlacedScanOrder
		if fieldOrder == FieldOrderFirst {
			height = height / 2
		} else {
			height = (height + 1) / 2
		}
	}

	widthMacroblocks := (frameHeader.Width + MacroblockWidth - 1) / MacroblockWidth
	heightMacroblocks := (height + MacroblockHeight - 1) / MacroblockHeight
	img := image.NewYCbCr(image.Rect(0, 0, widthMacroblocks*MacroblockWidth, heightMacroblocks*MacroblockHeight), frameHeader.Flags.SubsampleRatio())

	var header PictureHeader
	if err := header.Decode(r); err != nil {
		return nil, err
	}

	indexTableBuf := make([]byte, 2*header.NumberOfSlices)
	if _, err := r.ReadAt(indexTableBuf, header.HeaderSize); err != nil {
		return nil, err
	}

	sliceHeight := header.SliceHeightMacroblocks() * MacroblockHeight

	jobCh := make(chan *decodeSliceJob, header.NumberOfSlices)
	errCh := make(chan error, 1)

	var wg sync.WaitGroup

	const numberOfWorkers = 8

	wg.Add(numberOfWorkers)
	for i := 0; i < numberOfWorkers; i++ {
		go func() {
			defer wg.Done()
			decoder := NewSliceDecoder()
			for {
				job := <-jobCh
				if job == nil {
					return
				}
				r := io.NewSectionReader(r, job.offset, job.dataLen)
				rect := image.Rect(job.x, job.y, job.x+job.width, job.y+sliceHeight).Intersect(img.Bounds())
				if err := decoder.DecodeSlice(r, frameHeader, img, rect, scanOrder); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		}()
	}

	offset := header.HeaderSize + int64(len(indexTableBuf))
	x := 0
	y := 0

	for i := 0; i < header.NumberOfSlices; i++ {
		sliceDataLen := int64(binary.BigEndian.Uint16(indexTableBuf[i*2:]))
		sliceWidth := header.SliceWidthMacroblocks() * MacroblockWidth
		for sliceWidth > MacroblockWidth && x+sliceWidth > frameHeader.Width {
			sliceWidth >>= 1
		}
		jobCh <- &decodeSliceJob{
			offset:  offset,
			x:       x,
			y:       y,
			width:   sliceWidth,
			dataLen: sliceDataLen,
		}
		offset += sliceDataLen
		x += sliceWidth
		if x >= frameHeader.Width {
			x = 0
			y += sliceHeight
		}
	}

	for i := 0; i < numberOfWorkers; i++ {
		jobCh <- nil
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	return img.SubImage(image.Rect(0, 0, frameHeader.Width, height)), nil
}
