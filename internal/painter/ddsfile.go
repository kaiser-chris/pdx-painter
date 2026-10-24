package painter

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"math/bits"
	"os"
	"strings"
	"sync"

	"github.com/kaiser-chris/dds-go"
)

// DDS files are read and written with dds-go, which reads every format DirectX
// has and encodes all but a few. A map is written back the way it was read:
//
//   - in its own format, named the way DXGI names it, such as BC3_UNORM, or
//     Legacy for a legacy file whose pixels DXGI has no format for, such as
//     the games' 24 bit ones, which are written by their masks again;
//   - with as many mipmap levels;
//   - under the very header it was read with, where neither its format nor
//     its size changed: the flags, pitch and the tags its exporter left in the
//     reserved bytes, such as NVTT's, come back byte for byte.
//
// A map whose format is changed, or a new one, gets a header of dds-go's,
// legacy where one describes the format and DX10 otherwise, or DX10 where the
// map it replaces had one.

// Legacy is the format of a map whose pixels DXGI has no format for.
const Legacy = "Legacy"

// formats are dds-go's formats by their names, built the first time a name is
// looked up.
var (
	formatsOnce   sync.Once
	formatsByName map[string]dds.Format
)

// formatNamed is the DDS format of a name, as Format.String gives it.
func formatNamed(name string) (dds.Format, bool) {
	formatsOnce.Do(func() {
		formatsByName = map[string]dds.Format{}

		for value := dds.Format(1); value < 1<<12; value++ {
			if value.Known() {
				formatsByName[value.String()] = value
			}
		}
	})

	format, ok := formatsByName[name]

	return format, ok
}

// writable reports whether a map can be written in a format of a name: one of
// colour that dds-go encodes, or PNG or TGA. Depth, video and typeless
// formats, and integers, are no map's.
func writable(name string) bool {
	switch name {
	case "PNG", "TGA", Legacy:
		return true
	}

	format, ok := formatNamed(name)
	if !ok || !format.CanEncode() || format.Kind() == dds.KindInt || strings.Contains(name, "TYPELESS") {
		return false
	}

	return strings.HasPrefix(name, "BC") || strings.HasPrefix(name, "R") || strings.HasPrefix(name, "B") || name == "A8_UNORM"
}

// formatOf is the name of a parsed file's format.
func formatOf(file *dds.File) string {
	if file.Format == dds.FormatUnknown {
		return Legacy
	}

	return file.Format.String()
}

// oldFormats are the names the first manifests gave the formats.
var oldFormats = map[string]string{
	"BC1":   "BC1_UNORM",
	"BC3":   "BC3_UNORM",
	"BC7":   "BC7_UNORM",
	"BGRA8": "B8G8R8A8_UNORM",
	"RGBA8": "R8G8B8A8_UNORM",
}

// currentFormat is a format by the name a manifest of now gives it, for one
// of the first manifests, whose maps also said apart whether they are sRGB.
func currentFormat(name string, srgb bool) string {
	current, ok := oldFormats[name]
	if !ok {
		return name
	}

	if srgb && (name == "BC1" || name == "BC3" || name == "BC7" || name == "BGRA8" || name == "RGBA8") {
		current += "_SRGB"
	}

	return current
}

// readDDSPixels reads the largest level of a DDS file's first image as plain
// pixels, in eight bits a channel.
func readDDSPixels(data []byte) (*image.NRGBA, error) {
	file, err := dds.Parse(data)
	if err != nil {
		return nil, err
	}

	return file.Decode(0, 0)
}

// encodeDDS writes pixels as a DDS map, the way the map says: see above.
func encodeDDS(img *image.NRGBA, stored Map) ([]byte, error) {
	width, height := img.Rect.Dx(), img.Rect.Dy()

	var (
		original *dds.File
		data     []byte
	)

	if !stored.New {
		var err error
		if data, err = os.ReadFile(stored.Path); err != nil {
			return nil, err
		}

		if original, err = dds.Parse(data); err != nil {
			return nil, fmt.Errorf("%s: %w", stored.Path, err)
		}
	}

	levels := min(max(stored.Levels, 1), bits.Len(uint(max(width, height, 1))))

	if stored.Format == Legacy {
		if original == nil || original.Format != dds.FormatUnknown {
			return nil, fmt.Errorf("only a map read as a legacy file can be written as one")
		}

		description := original.Description()
		surfaces, err := encodeLevels(img, levels, func(level *image.NRGBA) ([]byte, error) {
			return dds.EncodeMaskedSurface(description.PixelFormat, description.Palette, level)
		}, false)
		if err != nil {
			return nil, err
		}

		return withHeader(data, original, description, width, height, levels, surfaces)
	}

	format, ok := formatNamed(stored.Format)
	if !ok || !writable(stored.Format) {
		return nil, fmt.Errorf("%s is not a format this tool writes", stored.Format)
	}

	surfaces, err := encodeLevels(img, levels, func(level *image.NRGBA) ([]byte, error) {
		return dds.EncodeSurfaceQuality(format, level, dds.QualityDefault)
	}, format.SRGB())
	if err != nil {
		return nil, err
	}

	description := dds.Description{Format: format, Dimension: dds.Texture2D, Width: width, Height: height, MipLevels: levels}

	if original != nil {
		if original.Format == format {
			return withHeader(data, original, original.Description(), width, height, levels, surfaces)
		}

		description.AlphaMode = original.AlphaMode
		description.DX10 = original.DX10 != nil
	}

	var out bytes.Buffer
	if err := dds.Write(&out, description, surfaces); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// withHeader puts surfaces under the header of the file they replace, where
// that header describes them as they are: of its size, and as many levels.
// Otherwise it writes a header of the file's description, of their size.
func withHeader(data []byte, original *dds.File, description dds.Description, width, height, levels int, surfaces []byte) ([]byte, error) {
	if original.Width == width && original.Height == height && original.MipLevels == levels &&
		original.Dimension == dds.Texture2D && original.ArraySize <= 1 {
		header := 128
		if original.DX10 != nil {
			header += 20
		}

		if size, err := description.Size(); err == nil && size == len(surfaces) {
			return append(append([]byte(nil), data[:header]...), surfaces...), nil
		}
	}

	description.Width, description.Height, description.MipLevels = width, height, levels
	description.Dimension, description.ArraySize, description.Faces = dds.Texture2D, 1, 0

	var out bytes.Buffer
	if err := dds.Write(&out, description, surfaces); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// encodeLevels encodes a chain of mipmap levels, each half the one before.
//
// The levels are averaged channel by channel, alpha with the rest rather
// than weighting them: the games keep data in alpha, the y of a normal or the
// roughness, which is no opacity to weight the other channels by. An sRGB
// format is averaged in linear light.
func encodeLevels(img *image.NRGBA, levels int, encode func(*image.NRGBA) ([]byte, error), srgb bool) ([]byte, error) {
	var surfaces []byte

	level := img
	for index := range levels {
		if index > 0 {
			level = halve(level, srgb)
		}

		surface, err := encode(level)
		if err != nil {
			return nil, err
		}

		surfaces = append(surfaces, surface...)
	}

	return surfaces, nil
}

// Halve averages an image down to half its size, each pixel the mean of the
// two by two it covers; a side of one pixel stays one.
func Halve(img *image.NRGBA) *image.NRGBA {
	return halve(img, false)
}

// halve averages an image down to half its size, its colour in linear light
// where it is sRGB, and its alpha as it is.
func halve(img *image.NRGBA, srgb bool) *image.NRGBA {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	half := image.NewNRGBA(image.Rect(0, 0, max(width/2, 1), max(height/2, 1)))

	for y := range half.Rect.Dy() {
		for x := range half.Rect.Dx() {
			var sum [4]float64

			for dy := range 2 {
				for dx := range 2 {
					sx := min(x*2+dx, width-1)
					sy := min(y*2+dy, height-1)
					offset := sy*img.Stride + sx*4

					for c := range 4 {
						value := float64(img.Pix[offset+c])
						if srgb && c < 3 {
							value = toLinear(value)
						}

						sum[c] += value
					}
				}
			}

			offset := y*half.Stride + x*4
			for c := range 4 {
				value := sum[c] / 4
				if srgb && c < 3 {
					value = fromLinear(value)
				}

				half.Pix[offset+c] = uint8(math.Round(min(max(value, 0), 255)))
			}
		}
	}

	return half
}

func toLinear(value float64) float64 {
	v := value / 255
	if v <= 0.04045 {
		return v / 12.92 * 255
	}

	return math.Pow((v+0.055)/1.055, 2.4) * 255
}

func fromLinear(value float64) float64 {
	v := value / 255
	if v <= 0.0031308 {
		return v * 12.92 * 255
	}

	return (1.055*math.Pow(v, 1/2.4) - 0.055) * 255
}

// WritableFormats are the names of every format a map can be written in, as
// a plan names them.
func WritableFormats() []string {
	names := []string{"PNG", "TGA"}

	for value := dds.Format(1); value < 1<<12; value++ {
		if value.Known() && writable(value.String()) {
			names = append(names, value.String())
		}
	}

	return names
}
