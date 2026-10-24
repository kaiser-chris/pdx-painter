package painter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaiser-chris/pdx-asset-go/texture"

	"github.com/kaiser-chris/dds-go"
)

// readPixels reads a texture file as plain pixels, whatever it is stored as.
//
// A DDS file is read by dds-go, in any format it has. One dds-go refuses, such
// as one holding fewer mipmap levels than it claims, is read the way the
// games' loader in pdx-asset-go reads it.
func readPixels(path string) (*image.NRGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if strings.EqualFold(filepath.Ext(path), ".dds") {
		if img, err := readDDSPixels(data); err == nil {
			return img, nil
		}
	}

	decoded, err := texture.Decode(path, data)
	if err != nil {
		return nil, err
	}

	img := image.NewNRGBA(image.Rect(0, 0, decoded.Width, decoded.Height))

	switch {
	case decoded.Format == texture.RGBA8:
		copy(img.Pix, decoded.Data[:decoded.Width*decoded.Height*4])

	case decoded.Format.Compressed():
		for y := range decoded.Height {
			for x := range decoded.Width {
				colour, ok := decoded.Colour(x, y)
				if !ok {
					return nil, fmt.Errorf("pixel %d,%d of %s cannot be read", x, y, path)
				}

				copy(img.Pix[y*img.Stride+x*4:], colour[:])
			}
		}

	default:
		return nil, fmt.Errorf("%s is stored as %v, which this tool does not edit", path, decoded.Format)
	}

	return img, nil
}

// storage says how a texture file is written back: in the format and with
// the levels it was read with.
func storage(path string) (Map, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".dds":
		data, err := os.ReadFile(path)
		if err != nil {
			return Map{}, err
		}

		file, err := dds.Parse(data)
		if err != nil {
			return Map{}, fmt.Errorf("%s: %w", path, err)
		}

		if file.Format != dds.FormatUnknown && !writable(file.Format.String()) {
			return Map{}, fmt.Errorf("%s is stored as %v, which can be read but not written back", path, file.Format)
		}

		return Map{Format: formatOf(file), Levels: file.MipLevels}, nil
	case ".png":
		return Map{Format: "PNG", Levels: 1}, nil
	case ".tga":
		return Map{Format: "TGA", Levels: 1}, nil
	}

	return Map{}, fmt.Errorf("%s is of no format this tool writes", path)
}

// encode writes pixels in a map's format.
func encode(img *image.NRGBA, stored Map) ([]byte, error) {
	switch stored.Format {
	case "PNG":
		var out bytes.Buffer
		err := png.Encode(&out, img)

		return out.Bytes(), err
	case "TGA":
		return encodeTGA(img), nil
	}

	return encodeDDS(img, stored)
}

// encodeTGA writes an uncompressed Targa of 32 bits a pixel, its first row at
// the top.
func encodeTGA(img *image.NRGBA) []byte {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, 18, 18+width*height*4)

	out[2] = 2 // uncompressed true colour
	binary.LittleEndian.PutUint16(out[12:], uint16(width))
	binary.LittleEndian.PutUint16(out[14:], uint16(height))
	out[16] = 32
	out[17] = 8 | 0x20 // eight bits of alpha, top to bottom

	for y := range height {
		for x := range width {
			p := img.Pix[y*img.Stride+x*4:]
			out = append(out, p[2], p[1], p[0], p[3])
		}
	}

	return out
}

// resize brings an image to a size: halved while it is twice as large or
// more, then sampled between its pixels the rest of the way.
func resize(img *image.NRGBA, width, height int) *image.NRGBA {
	for img.Rect.Dx() >= width*2 && img.Rect.Dy() >= height*2 {
		img = Halve(img)
	}

	if img.Rect.Dx() == width && img.Rect.Dy() == height {
		return img
	}

	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	sw, sh := img.Rect.Dx(), img.Rect.Dy()

	for y := range height {
		fy := max((float64(y)+0.5)*float64(sh)/float64(height)-0.5, 0)
		y0 := min(int(fy), sh-1)
		y1 := min(y0+1, sh-1)
		ty := fy - float64(y0)

		for x := range width {
			fx := max((float64(x)+0.5)*float64(sw)/float64(width)-0.5, 0)
			x0 := min(int(fx), sw-1)
			x1 := min(x0+1, sw-1)
			tx := fx - float64(x0)

			for c := range 4 {
				at := func(px, py int) float64 { return float64(img.Pix[py*img.Stride+px*4+c]) }
				top := at(x0, y0)*(1-tx) + at(x1, y0)*tx
				bottom := at(x0, y1)*(1-tx) + at(x1, y1)*tx
				out.Pix[y*out.Stride+x*4+c] = uint8(top*(1-ty) + bottom*ty + 0.5)
			}
		}
	}

	return out
}

// readPNG reads an image Painter exported, as plain pixels: a grey one with
// its grey in every channel of colour.
func readPNG(path string) (*image.NRGBA, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoded, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if img, ok := decoded.(*image.NRGBA); ok && img.Rect.Min == (image.Point{}) {
		return img, nil
	}

	bounds := decoded.Bounds()
	img := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(img, img.Bounds(), decoded, bounds.Min, draw.Src)

	return img, nil
}

func writePNG(path string, img image.Image) error {
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return err
	}

	return os.WriteFile(path, out.Bytes(), 0o644)
}
