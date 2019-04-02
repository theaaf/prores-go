package prores

import (
	"bytes"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeFrame(t *testing.T) {
	t.Run("Skycam", func(t *testing.T) {
		buf, err := ioutil.ReadFile("testdata/skycam-frame.icpf")
		require.NoError(t, err)
		r := bytes.NewReader(buf)
		_, err = DecodeFrame(r, int64(len(buf)))
		assert.NoError(t, err)
	})

	t.Run("BIR-ATL-Interlaced", func(t *testing.T) {
		buf, err := ioutil.ReadFile("testdata/bir-atl-interlaced-frame.icpf")
		require.NoError(t, err)
		r := bytes.NewReader(buf)
		img, err := DecodeFrame(r, int64(len(buf)))
		assert.NoError(t, err)
		assert.Equal(t, 1920, img.Bounds().Dx())
		assert.Equal(t, 540, img.Bounds().Dy())
	})
}

func benchmarkDecodeFrame(b *testing.B, path string) {
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	r := bytes.NewReader(buf)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := DecodeFrame(r, int64(len(buf))); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeFrame_Sintel(b *testing.B) {
	benchmarkDecodeFrame(b, "testdata/sintel-frame.icpf")
}

func BenchmarkDecodeFrame_Skycam(b *testing.B) {
	benchmarkDecodeFrame(b, "testdata/skycam-frame.icpf")
}
