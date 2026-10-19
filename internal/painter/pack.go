package painter

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// PackOptions say where packing reads from and writes to.
type PackOptions struct {
	// Exported is the folder Painter exported the channels to, each as the
	// Texture Set's name and the channel's ID: wood_basecolor.png.
	Exported string

	// Out is the folder the maps are written straight into, each by its own
	// file name, without the folders it was found in.
	Out string

	// InPlace writes every map over the file it was read from, and a new map
	// where it was planned, next to its mesh, rather than below Out.
	InPlace bool

	// Backup copies a file that is about to be written over to the same name
	// with .bak after it, once: the first backup is the original.
	Backup bool

	// DryRun works out what would be written without writing it.
	DryRun bool
}

// Packed is what packing did.
type Packed struct {
	// Written are the files written, and Unchanged the maps Painter gave
	// back as they were read, which are not written again: every write of a
	// compressed texture loses a little.
	Written   []string `json:"written"`
	Unchanged []string `json:"unchanged"`

	// Notes say what was left as it was and why, such as a channel Painter
	// did not export.
	Notes []string `json:"notes"`
}

// target is a map being packed: the original's pixels with what Painter
// exported laid over them.
type target struct {
	stored   Map
	original *image.NRGBA
	pixels   *image.NRGBA
}

// Pack lays the channels Painter exported into the maps they came from and
// writes the maps.
//
// Two Texture Sets drawn with one map both lay their channels into it, the
// later over the earlier.
func Pack(manifest *Manifest, options PackOptions) (*Packed, error) {
	if manifest.Version != Version {
		return nil, fmt.Errorf("the manifest is of version %d, this tool reads %d", manifest.Version, Version)
	}

	if options.Out == "" && !options.InPlace {
		return nil, fmt.Errorf("no folder to write the textures below")
	}

	packed := &Packed{Written: []string{}, Unchanged: []string{}, Notes: []string{}}
	targets := map[string]*target{}

	var order []string

	for _, set := range manifest.TextureSets {
		mask := readMask(set.Coverage)
		if mask == nil {
			packed.Notes = append(packed.Notes, fmt.Sprintf(
				"the coverage of %s cannot be read; its whole export is laid into its maps, over what other models may share of them", set.Name))
		}

		for _, channel := range set.Channels {
			s, ok := specOf(channel.ID)
			if !ok {
				return nil, fmt.Errorf("texture set %s has a channel %s this tool does not know", set.Name, channel.ID)
			}

			stored, ok := set.Maps[channel.Map]
			if !ok {
				return nil, fmt.Errorf("texture set %s has no %s map for its channel %s", set.Name, channel.Map, channel.ID)
			}

			exported := filepath.Join(options.Exported, set.Name+"_"+channel.ID+".png")
			if _, err := os.Stat(exported); err != nil {
				packed.Notes = append(packed.Notes, fmt.Sprintf("%s of %s was not exported; kept as it was", channel.ID, set.Name))

				continue
			}

			img, err := readPNG(exported)
			if err != nil {
				return nil, err
			}

			into, ok := targets[stored.Path]
			if !ok {
				into = &target{stored: stored}

				if stored.New {
					// A new map is as large as Painter painted it.
					into.pixels = blank(channel.Map, img.Rect.Dx(), img.Rect.Dy())
				} else {
					if into.original, err = readPixels(stored.Path); err != nil {
						return nil, err
					}

					into.pixels = clone(into.original)
				}

				targets[stored.Path] = into
				order = append(order, stored.Path)
			}

			img = resize(img, into.pixels.Rect.Dx(), into.pixels.Rect.Dy())

			// A new map is no other model's: the whole of it is taken.
			if mask == nil || stored.New {
				s.insert(into.pixels, img, manifest.FlipGreen)

				continue
			}

			// Laid into a copy, of which only what the mesh covers is
			// taken.
			layer := clone(into.pixels)
			s.insert(layer, img, manifest.FlipGreen)
			takeCovered(into.pixels, layer, mask)
		}
	}

	// Two maps of one name from different folders would land on one file in
	// a folder of their own, which is refused before anything is written.
	destinations := map[string]string{}

	for _, path := range order {
		stored := targets[path].stored

		destination := stored.Path
		if !options.InPlace {
			destination = filepath.Join(options.Out, filepath.Base(filepath.FromSlash(stored.Relative)))
		}

		key := strings.ToLower(filepath.Clean(destination))
		if other, ok := destinations[key]; ok {
			return nil, fmt.Errorf("%s and %s would both be written to %s; save them over the files they came from instead", other, stored.Relative, destination)
		}

		destinations[key] = stored.Relative
	}

	for _, path := range order {
		into := targets[path]

		if into.original != nil && bytes.Equal(into.pixels.Pix, into.original.Pix) {
			packed.Unchanged = append(packed.Unchanged, into.stored.Relative)

			continue
		}

		destination := into.stored.Path
		if !options.InPlace {
			destination = filepath.Join(options.Out, filepath.Base(filepath.FromSlash(into.stored.Relative)))
		}

		data, err := encode(into.pixels, into.stored)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", into.stored.Relative, err)
		}

		if !options.DryRun {
			if err := write(destination, data, options.Backup); err != nil {
				return nil, err
			}
		}

		packed.Written = append(packed.Written, destination)
	}

	slices.Sort(packed.Unchanged)

	return packed, nil
}

// blank is a new map of a kind before anything is laid into it.
func blank(kind string, width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	pixel := blanks[kind]

	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:i+4], pixel[:])
	}

	return img
}

// readMask reads a coverage mask, or nil for one that cannot be read.
func readMask(path string) *image.Gray {
	if path == "" {
		return nil
	}

	img, err := readPNG(path)
	if err != nil {
		return nil
	}

	mask := image.NewGray(img.Rect)
	for i := range mask.Pix {
		mask.Pix[i] = img.Pix[i*4]
	}

	return mask
}

// takeCovered copies the texels a mask covers from one image into another of
// the same size.
func takeCovered(into, from *image.NRGBA, mask *image.Gray) {
	width, height := into.Rect.Dx(), into.Rect.Dy()

	for y := range height {
		for x := range width {
			if covered(mask, x, y, width, height) {
				at := y*into.Stride + x*4
				copy(into.Pix[at:at+4], from.Pix[at:at+4])
			}
		}
	}
}

func clone(img *image.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(img.Rect)
	copy(out.Pix, img.Pix)

	return out
}

// write writes a file, making its folder, and keeps the file it replaces as
// a backup if it is asked to and there is none yet.
func write(path string, data []byte, backup bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	if backup {
		if _, err := os.Stat(path + ".bak"); os.IsNotExist(err) {
			if err := copyFile(path, path+".bak"); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("back up %s: %w", path, err)
			}
		}
	}

	// Written next to the file and moved over it, so that a write that fails
	// halfway leaves the original as it was.
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}

	return os.Rename(temporary, path)
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(to)
	if err != nil {
		return err
	}

	if _, err := io.Copy(destination, source); err != nil {
		destination.Close()

		return err
	}

	return destination.Close()
}
