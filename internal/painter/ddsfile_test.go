package painter

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/kaiser-chris/dds-go"
)

// original writes a DDS file the way an exporter of the games would: in a
// format, with every level, under a header that carries a tag of the
// exporter's in its reserved bytes, as NVTT's does.
func original(t *testing.T, img *image.NRGBA, description dds.Description, masked bool) []byte {
	t.Helper()

	description.Width, description.Height = img.Rect.Dx(), img.Rect.Dy()
	description.MipLevels = 1

	var surface []byte
	var err error

	if masked {
		surface, err = dds.EncodeMaskedSurface(description.PixelFormat, nil, img)
	} else {
		surface, err = dds.EncodeSurface(description.Format, img)
	}

	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := dds.Write(&out, description, surface); err != nil {
		t.Fatal(err)
	}

	data := out.Bytes()
	copy(data[32+9*4:], "NVTT") // a reserved word, as NVTT tags its files

	return data
}

// The formats a map is read in are the formats it is written in, under the
// header it was read with: the exporter's tag in it as well.
func TestEncodeDDSKeepsTheFormatAndHeader(t *testing.T) {
	img := pattern(8, 90)

	cases := []struct {
		name        string
		description dds.Description
		masked      bool
		want        string
	}{
		{"DXT1", dds.Description{Format: dds.BC1Unorm}, false, "BC1_UNORM"},
		{"DXT3", dds.Description{Format: dds.BC2Unorm}, false, "BC2_UNORM"},
		{"DXT5", dds.Description{Format: dds.BC3Unorm}, false, "BC3_UNORM"},
		{"BC4", dds.Description{Format: dds.BC4Unorm}, false, "BC4_UNORM"},
		{"BC5", dds.Description{Format: dds.BC5Unorm}, false, "BC5_UNORM"},
		{"BC7 sRGB", dds.Description{Format: dds.BC7UnormSRGB}, false, "BC7_UNORM_SRGB"},
		{"BGRA8", dds.Description{Format: dds.B8G8R8A8Unorm}, false, "B8G8R8A8_UNORM"},
		{"RGBA8 behind DX10", dds.Description{Format: dds.R8G8B8A8Unorm, DX10: true}, false, "R8G8B8A8_UNORM"},
		{"B5G6R5", dds.Description{Format: dds.B5G6R5Unorm}, false, "B5G6R5_UNORM"},
		{"24 bit legacy", dds.Description{PixelFormat: dds.PixelFormat{
			Size: 32, Flags: 0x40, RGBBitCount: 24, RBitMask: 0xff0000, GBitMask: 0xff00, BBitMask: 0xff,
		}}, true, Legacy},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "map.dds")
			data := original(t, img, c.description, c.masked)

			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}

			stored, err := storage(path)
			if err != nil {
				t.Fatal(err)
			}

			if stored.Format != c.want {
				t.Fatalf("read as %s, want %s", stored.Format, c.want)
			}

			stored.Path = path

			pixels, err := readPixels(path)
			if err != nil {
				t.Fatal(err)
			}

			written, err := encodeDDS(pixels, stored)
			if err != nil {
				t.Fatal(err)
			}

			header := 128
			if c.description.DX10 || c.description.Format == dds.BC7UnormSRGB {
				header += 20
			}

			if !bytes.Equal(written[:header], data[:header]) {
				t.Errorf("the header changed:\n got %x\nwant %x", written[:header], data[:header])
			}

			if len(written) != len(data) {
				t.Errorf("%d bytes written, want the %d read", len(written), len(data))
			}

			again, err := dds.Parse(written)
			if err != nil {
				t.Fatal(err)
			}

			if formatOf(again) != c.want {
				t.Errorf("written as %s, want %s", formatOf(again), c.want)
			}
		})
	}
}

// A map whose format is changed gets a header of its new format, behind a DX10
// header where the format has no other, and keeps its mipmap levels.
func TestEncodeDDSChangesTheFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "map.dds")
	if err := os.WriteFile(path, encoded(t, pattern(16, 90), dds.BC3Unorm), 0o644); err != nil {
		t.Fatal(err)
	}

	stored, err := storage(path)
	if err != nil {
		t.Fatal(err)
	}

	stored.Path = path

	for _, format := range []string{"BC7_UNORM", "BC5_UNORM", "BC1_UNORM", "R8G8B8A8_UNORM_SRGB"} {
		changed := stored
		changed.Format = format

		written, err := encodeDDS(pattern(16, 90), changed)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}

		file, err := dds.Parse(written)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}

		if formatOf(file) != format || file.MipLevels != 5 || file.Width != 16 {
			t.Errorf("%s: written as %v of %d with %d levels", format, file.Format, file.Width, file.MipLevels)
		}
	}
}

// A new map has no header to keep: it gets dds-go's, legacy for DXT5 as the
// games' own maps have.
func TestEncodeDDSOfANewMap(t *testing.T) {
	written, err := encodeDDS(pattern(32, 90), Map{Format: newFormat, Levels: 99, New: true})
	if err != nil {
		t.Fatal(err)
	}

	file, err := dds.Parse(written)
	if err != nil {
		t.Fatal(err)
	}

	if file.Format != dds.BC3Unorm || file.DX10 != nil || file.MipLevels != 6 || string(written[84:88]) != "DXT5" {
		t.Errorf("a new map is %v, DX10 %v, %d levels, %q", file.Format, file.DX10 != nil, file.MipLevels, written[84:88])
	}
}

// The levels below the largest average every channel on its own: the games
// keep data in alpha, which weighting the colour by would change.
func TestMipmapsDoNotWeightByAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	copy(img.Pix, []byte{
		200, 100, 0, 0, 0, 100, 200, 255,
		200, 100, 0, 0, 0, 100, 200, 255,
	})

	half := halve(img, false)
	if got := [4]byte(half.Pix); got != [4]byte{100, 100, 100, 128} {
		t.Errorf("averaged %v, want each channel's own mean", got)
	}

	// In linear light, the mean of black and white is brighter than 128.
	grey := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	copy(grey.Pix, []byte{0, 0, 0, 255, 255, 255, 255, 255})

	if got := halve(grey, true).Pix[0]; got < 180 || got > 190 {
		t.Errorf("sRGB average %d, want about 188", got)
	}
}

// The first manifests named the formats otherwise.
func TestUpgradeTheFirstManifests(t *testing.T) {
	manifest := &Manifest{Version: FirstVersion, TextureSets: []TextureSet{{Maps: map[string]Map{
		Diffuse:    {Format: "BC7", SRGB: true},
		Normal:     {Format: "BC3"},
		Properties: {Format: "PNG"},
	}}}}

	manifest.upgrade()

	maps := manifest.TextureSets[0].Maps
	if maps[Diffuse].Format != "BC7_UNORM_SRGB" || maps[Normal].Format != "BC3_UNORM" || maps[Properties].Format != "PNG" || manifest.Version != Version {
		t.Errorf("upgraded to %+v", maps)
	}
}
