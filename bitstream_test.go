package prores

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBitstream_ReadSmallUnary(t *testing.T) {
	bs := &Bitstream{
		Bytes: []byte{0x00, 0x08, 0x80, 0, 0, 0, 0, 1, 1},
	}

	var n int
	assert.True(t, bs.ReadSmallUnary(&n))
	assert.Equal(t, 12, n)
	assert.True(t, bs.ReadSmallUnary(&n))
	assert.Equal(t, 3, n)
	assert.True(t, bs.ReadSmallUnary(&n))
	assert.Equal(t, 46, n)
	assert.True(t, bs.ReadSmallUnary(&n))
	assert.Equal(t, 7, n)

	assert.False(t, bs.ReadSmallUnary(&n))
}

func TestBitstream_ReadInt(t *testing.T) {
	bs := &Bitstream{
		Bytes: []byte{0x08, 0x08},
	}

	var n int
	assert.True(t, bs.ReadInt(8, &n))
	assert.Equal(t, 0x08, n)

	assert.True(t, bs.ReadInt(6, &n))
	assert.Equal(t, 2, n)
}

func TestBitstream_ReadBit(t *testing.T) {
	bs := &Bitstream{
		Bytes: []byte{0x08},
	}

	var v bool
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.True(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
	assert.True(t, bs.ReadBit(&v))
	assert.False(t, v)
}
