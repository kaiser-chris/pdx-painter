package dds

import (
	"image"
	"math"
	"testing"

	"github.com/kaiser-chris/pdx-asset-go/texture"
)

// gradient is an image of smooth ramps in every channel, with a hard edge
// down the middle, which is what a game texture is made of.
func gradient(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for y := range height {
		for x := range width {
			offset := y*img.Stride + x*4
			img.Pix[offset] = uint8(x * 255 / max(width-1, 1))
			img.Pix[offset+1] = uint8(y * 255 / max(height-1, 1))
			img.Pix[offset+2] = uint8((x + y) * 127 / max(width+height-2, 1))

			if x < width/2 {
				img.Pix[offset+2] += 100
			}

			img.Pix[offset+3] = uint8(255 - y*200/max(height-1, 1))
		}
	}

	return img
}

// decoded reads back what Encode wrote with the decoder the tool reads the
// games' files with, every pixel of the largest level.
func decoded(t *testing.T, data []byte) *texture.Image {
	t.Helper()

	img, err := texture.DecodeDDS(data)
	if err != nil {
		t.Fatal(err)
	}

	return img
}

// psnr is how close two images are, channel by channel, in decibels.
func psnr(t *testing.T, want *image.NRGBA, got *texture.Image, channels int) float64 {
	t.Helper()

	sum, count := 0.0, 0

	for y := range want.Rect.Dy() {
		for x := range want.Rect.Dx() {
			colour, ok := got.Colour(x, y)
			if !ok {
				t.Fatalf("pixel %d,%d cannot be read from %v", x, y, got.Format)
			}

			for c := range channels {
				d := float64(want.Pix[y*want.Stride+x*4+c]) - float64(colour[c])
				sum += d * d
				count++
			}
		}
	}

	if sum == 0 {
		return math.Inf(1)
	}

	return 10 * math.Log10(255*255/(sum/float64(count)))
}

func TestEncodeEveryFormat(t *testing.T) {
	source := gradient(64, 32)

	cases := []struct {
		format   Format
		channels int
		least    float64
	}{
		{BC1, 3, 30},
		{BC3, 4, 32},
		{BC7, 4, 38},
		{BGRA8, 4, math.Inf(1)},
		{RGBA8, 4, math.Inf(1)},
	}

	// DXT1 holds no alpha but a transparent pixel, which the gradient's
	// alpha would make of half its pixels.
	opaque := gradient(64, 32)
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 255
	}

	for _, c := range cases {
		t.Run(string(c.format), func(t *testing.T) {
			source := source
			if c.format == BC1 {
				source = opaque
			}

			data, err := Encode(source, c.format, 99, false)
			if err != nil {
				t.Fatal(err)
			}

			header, err := ReadHeader(data)
			if err != nil {
				t.Fatal(err)
			}

			// Ninety nine levels asked for, seven are what 64 pixels have.
			if header.Format != c.format || header.Width != 64 || header.Height != 32 || header.Levels != 7 {
				t.Errorf("header = %+v", header)
			}

			img := decoded(t, data)
			if img.Width != 64 || img.Height != 32 {
				t.Errorf("decoded %dx%d", img.Width, img.Height)
			}

			if quality := psnr(t, source, img, c.channels); quality < c.least {
				t.Errorf("PSNR %.1f dB, want at least %.1f", quality, c.least)
			}
		})
	}
}

// A block of one colour comes back as that colour, as near as its bits hold
// it: within one in BC7, whose ends share their lowest bit between the
// channels, and exactly in DXT5's alpha.
func TestEncodeFlatBlocks(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{200, 17, 99, 254})
	}

	data, _ := Encode(img, BC7, 1, false)
	colour, _ := decoded(t, data).Colour(2, 2)

	for c, want := range []int{200, 17, 99, 254} {
		if d := int(colour[c]) - want; d < -1 || d > 1 {
			t.Errorf("BC7 decoded %v", colour)
		}
	}

	data, _ = Encode(img, BC3, 1, false)
	if colour, _ := decoded(t, data).Colour(2, 2); colour[3] != 254 {
		t.Errorf("BC3 alpha %d", colour[3])
	}

	// A colour every channel of which is odd, or even, is exact in BC7.
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{201, 17, 99, 255})
	}

	data, _ = Encode(img, BC7, 1, false)
	if colour, _ := decoded(t, data).Colour(1, 3); colour != [4]byte{201, 17, 99, 255} {
		t.Errorf("BC7 decoded %v", colour)
	}
}

// DXT1 keeps the pixels that are transparent transparent, in its mode of
// three colours.
func TestEncodeTransparentDXT1(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{10, 200, 30, 255})
	}

	img.Pix[3] = 0

	data, _ := Encode(img, BC1, 1, false)

	// The library reads a DXT1 file as transparent only when its header says
	// so; the block itself is what matters, read here through its palette.
	data[80] |= pixelAlpha

	got := decoded(t, data)
	first, _ := got.Colour(0, 0)
	second, _ := got.Colour(1, 0)

	if first[3] != 0 || second[3] != 255 {
		t.Errorf("alpha %d and %d, want 0 and 255", first[3], second[3])
	}
}

// Sides that are not a multiple of four, and the levels down to one pixel,
// are written whole.
func TestEncodeOddSizes(t *testing.T) {
	data, err := Encode(gradient(6, 3), BC7, 0, true)
	if err != nil {
		t.Fatal(err)
	}

	header, _ := ReadHeader(data)
	if !header.SRGB || header.Levels != 1 {
		t.Errorf("header = %+v", header)
	}

	if img := decoded(t, data); img.Width != 6 || img.Height != 3 {
		t.Errorf("decoded %dx%d", img.Width, img.Height)
	}
}

func TestReadHeaderRefusesWhatItCannotWrite(t *testing.T) {
	data, _ := Encode(gradient(4, 4), BC3, 1, false)
	copy(data[84:], "ATI2")

	if _, err := ReadHeader(data); err == nil {
		t.Error("a format of no writer was read")
	}

	if _, err := ReadHeader([]byte("DDS ")); err == nil {
		t.Error("a header cut short was read")
	}
}
