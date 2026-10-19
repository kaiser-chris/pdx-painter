// Package dds writes the DDS textures the Paradox games read, in the formats
// they ship: DXT1, DXT5 and BC7 blocks, and uncompressed 32 bit pixels.
//
// pdx-asset-go decodes all of those; this is the other direction, for writing
// back a texture edited elsewhere. A texture is written in the format it was
// read in, with as many mipmap levels, which Header reads off the original.
package dds

import (
	"encoding/binary"
	"fmt"
	"image"
	"math/bits"
)

// Format is the way a DDS file stores its pixels.
type Format string

const (
	// BC1 is DXT1: four bits a pixel, colour and one bit of alpha.
	BC1 Format = "BC1"

	// BC3 is DXT5: eight bits a pixel, colour and a smooth alpha. A DXT3
	// original is written as this too.
	BC3 Format = "BC3"

	// BC7 is eight bits a pixel of better quality, behind a DX10 header.
	BC7 Format = "BC7"

	// BGRA8 is uncompressed 32 bit pixels in the legacy header, blue first
	// in memory, which is what the games' uncompressed textures use.
	BGRA8 Format = "BGRA8"

	// RGBA8 is uncompressed 32 bit pixels behind a DX10 header.
	RGBA8 Format = "RGBA8"
)

// Header is what a DDS file says about how it is stored.
type Header struct {
	Width, Height int
	Format        Format
	Levels        int

	// SRGB is set for a file whose DX10 header names an sRGB format. The
	// legacy header has no way of saying so.
	SRGB bool
}

// Offsets and flags of the DDS header, see the DDS_HEADER of the DirectX
// documentation.
const (
	headerSize = 128 // the magic and the 124 byte header
	dx10Size   = 20

	flagCaps        = 0x1
	flagHeight      = 0x2
	flagWidth       = 0x4
	flagPitch       = 0x8
	flagPixelFormat = 0x1000
	flagMipmapCount = 0x20000
	flagLinearSize  = 0x80000

	pixelAlpha  = 0x1
	pixelFourCC = 0x4
	pixelRGB    = 0x40

	capsComplex = 0x8
	capsTexture = 0x1000
	capsMipmap  = 0x400000

	dxgiR8G8B8A8     = 28
	dxgiR8G8B8A8SRGB = 29
	dxgiBC1          = 71
	dxgiBC1SRGB      = 72
	dxgiBC2          = 74
	dxgiBC2SRGB      = 75
	dxgiBC3          = 77
	dxgiBC3SRGB      = 78
	dxgiBC7          = 98
	dxgiBC7SRGB      = 99
)

// ReadHeader reads how a DDS file is stored. Formats the games' models do not
// use, such as half floats, are refused: there is nothing to write them back
// in.
func ReadHeader(data []byte) (Header, error) {
	if len(data) < headerSize || string(data[0:4]) != "DDS " {
		return Header{}, fmt.Errorf("not a DDS file")
	}

	le := binary.LittleEndian
	header := Header{
		Height: int(le.Uint32(data[12:])),
		Width:  int(le.Uint32(data[16:])),
		Levels: max(int(le.Uint32(data[28:])), 1),
	}

	flags := le.Uint32(data[80:])
	fourCC := string(data[84:88])

	switch {
	case flags&pixelFourCC != 0 && fourCC == "DX10":
		if len(data) < headerSize+dx10Size {
			return Header{}, fmt.Errorf("the DX10 header is cut short")
		}

		switch dxgi := le.Uint32(data[128:]); dxgi {
		case dxgiBC1, dxgiBC1SRGB:
			header.Format, header.SRGB = BC1, dxgi == dxgiBC1SRGB
		case dxgiBC2, dxgiBC2SRGB, dxgiBC3, dxgiBC3SRGB:
			header.Format, header.SRGB = BC3, dxgi == dxgiBC2SRGB || dxgi == dxgiBC3SRGB
		case dxgiBC7, dxgiBC7SRGB:
			header.Format, header.SRGB = BC7, dxgi == dxgiBC7SRGB
		case dxgiR8G8B8A8, dxgiR8G8B8A8SRGB:
			header.Format, header.SRGB = RGBA8, dxgi == dxgiR8G8B8A8SRGB
		default:
			return Header{}, fmt.Errorf("DXGI format %d is not one this tool writes", dxgi)
		}

	case flags&pixelFourCC != 0:
		switch fourCC {
		case "DXT1":
			header.Format = BC1
		case "DXT2", "DXT3", "DXT4", "DXT5":
			header.Format = BC3
		default:
			return Header{}, fmt.Errorf("format %q is not one this tool writes", fourCC)
		}

	case flags&pixelRGB != 0:
		header.Format = BGRA8

	default:
		return Header{}, fmt.Errorf("pixel format flags %#x are not one this tool writes", flags)
	}

	if header.Width <= 0 || header.Height <= 0 {
		return Header{}, fmt.Errorf("the size %dx%d is not an image", header.Width, header.Height)
	}

	return header, nil
}

// MaxLevels is how many mipmap levels an image of a size has, down to one
// pixel.
func MaxLevels(width, height int) int {
	return bits.Len(uint(max(width, height, 1)))
}

// Encode writes an image as a DDS file in a format, with a number of mipmap
// levels, the smaller ones made from the image by averaging. The levels are
// held to what the size has.
func Encode(img *image.NRGBA, format Format, levels int, srgb bool) ([]byte, error) {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("the image is empty")
	}

	levels = min(max(levels, 1), MaxLevels(width, height))

	var body []byte
	level := img

	for index := range levels {
		if index > 0 {
			level = Halve(level)
		}

		switch format {
		case BC1, BC3, BC7:
			body = append(body, compress(level, format)...)
		case BGRA8:
			body = append(body, swizzle(level, true)...)
		case RGBA8:
			body = append(body, swizzle(level, false)...)
		default:
			return nil, fmt.Errorf("format %q is not one this tool writes", format)
		}
	}

	return append(header(width, height, format, levels, srgb), body...), nil
}

func header(width, height int, format Format, levels int, srgb bool) []byte {
	le := binary.LittleEndian
	out := make([]byte, headerSize, headerSize+dx10Size)

	copy(out, "DDS ")
	le.PutUint32(out[4:], 124)

	flags := uint32(flagCaps | flagHeight | flagWidth | flagPixelFormat)
	if levels > 1 {
		flags |= flagMipmapCount
	}

	compressed := format == BC1 || format == BC3 || format == BC7
	if compressed {
		flags |= flagLinearSize
		le.PutUint32(out[20:], uint32(blocksOf(width)*blocksOf(height)*blockSize(format)))
	} else {
		flags |= flagPitch
		le.PutUint32(out[20:], uint32(width*4))
	}

	le.PutUint32(out[8:], flags)
	le.PutUint32(out[12:], uint32(height))
	le.PutUint32(out[16:], uint32(width))
	le.PutUint32(out[28:], uint32(levels))

	// The pixel format.
	le.PutUint32(out[76:], 32)

	switch format {
	case BC1:
		le.PutUint32(out[80:], pixelFourCC)
		copy(out[84:], "DXT1")
	case BC3:
		le.PutUint32(out[80:], pixelFourCC)
		copy(out[84:], "DXT5")
	case BGRA8:
		le.PutUint32(out[80:], pixelRGB|pixelAlpha)
		le.PutUint32(out[88:], 32)
		le.PutUint32(out[92:], 0x00ff0000)
		le.PutUint32(out[96:], 0x0000ff00)
		le.PutUint32(out[100:], 0x000000ff)
		le.PutUint32(out[104:], 0xff000000)
	case BC7, RGBA8:
		le.PutUint32(out[80:], pixelFourCC)
		copy(out[84:], "DX10")
	}

	caps := uint32(capsTexture)
	if levels > 1 {
		caps |= capsComplex | capsMipmap
	}

	le.PutUint32(out[108:], caps)

	if format == BC7 || format == RGBA8 {
		dxgi := uint32(dxgiBC7)
		if format == RGBA8 {
			dxgi = dxgiR8G8B8A8
		}

		if srgb {
			dxgi++
		}

		out = out[:headerSize+dx10Size]
		le.PutUint32(out[128:], dxgi)
		le.PutUint32(out[132:], 3) // a 2D texture
		le.PutUint32(out[136:], 0)
		le.PutUint32(out[140:], 1) // one in the array
		le.PutUint32(out[144:], 0)
	}

	return out
}

func blocksOf(size int) int {
	return (size + 3) / 4
}

func blockSize(format Format) int {
	if format == BC1 {
		return 8
	}

	return 16
}

// swizzle lays the pixels out uncompressed, blue first for the legacy header.
func swizzle(img *image.NRGBA, bgra bool) []byte {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, 0, width*height*4)

	for y := range height {
		row := img.Pix[y*img.Stride : y*img.Stride+width*4]

		for x := 0; x < len(row); x += 4 {
			if bgra {
				out = append(out, row[x+2], row[x+1], row[x], row[x+3])
			} else {
				out = append(out, row[x:x+4]...)
			}
		}
	}

	return out
}

// compress encodes one level as blocks of four by four pixels. A block that
// runs over the edge of a level smaller than four repeats its last pixels.
func compress(img *image.NRGBA, format Format) []byte {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	size := blockSize(format)
	out := make([]byte, blocksOf(width)*blocksOf(height)*size)

	var block [16][4]uint8

	at := 0

	for by := 0; by < height; by += 4 {
		for bx := 0; bx < width; bx += 4 {
			for y := range 4 {
				for x := range 4 {
					px := min(bx+x, width-1)
					py := min(by+y, height-1)
					offset := py*img.Stride + px*4
					copy(block[y*4+x][:], img.Pix[offset:offset+4])
				}
			}

			switch format {
			case BC1:
				encodeBC1(&block, out[at:at+8])
			case BC3:
				encodeBC3(&block, out[at:at+16])
			case BC7:
				encodeBC7(&block, out[at:at+16])
			}

			at += size
		}
	}

	return out
}

// Halve averages an image down to half its size, each pixel the mean of the
// two by two it covers; a side of one pixel stays one.
func Halve(img *image.NRGBA) *image.NRGBA {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	half := image.NewNRGBA(image.Rect(0, 0, max(width/2, 1), max(height/2, 1)))

	for y := range half.Rect.Dy() {
		for x := range half.Rect.Dx() {
			var sum [4]int

			for dy := range 2 {
				for dx := range 2 {
					sx := min(x*2+dx, width-1)
					sy := min(y*2+dy, height-1)
					offset := sy*img.Stride + sx*4

					for c := range 4 {
						sum[c] += int(img.Pix[offset+c])
					}
				}
			}

			offset := y*half.Stride + x*4
			for c := range 4 {
				half.Pix[offset+c] = uint8((sum[c] + 2) / 4)
			}
		}
	}

	return half
}
