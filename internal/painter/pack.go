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

	// Plan says what is written, where and how, in place of Out and InPlace:
	// the maps chosen, each by a name and in a format of its own, and a copy
	// of the mesh and an asset for them, which is how an entity changed is
	// saved as a new one. See plan.go.
	Plan *Plan
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
// exported laid over them, and where and how it is written.
type target struct {
	stored   Map
	original *image.NRGBA
	pixels   *image.NRGBA

	destination string
	format      string
}

// Pack lays the channels Painter exported into the maps they came from and
// writes the maps, and with a plan, the mesh and an asset as well.
//
// Two Texture Sets drawn with one map both lay their channels into it, the
// later over the earlier.
func Pack(manifest *Manifest, options PackOptions) (*Packed, error) {
	switch manifest.Version {
	case Version:
	case FirstVersion:
		manifest.upgrade()
	default:
		return nil, fmt.Errorf("the manifest is of version %d, this tool reads %d", manifest.Version, Version)
	}

	if options.Plan != nil {
		if err := options.Plan.check(manifest); err != nil {
			return nil, err
		}
	} else if options.Out == "" && !options.InPlace {
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

			destination, format, wanted := options.where(set.Name, channel.Map, stored)
			if !wanted {
				continue
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
				into = &target{stored: stored, destination: destination, format: format}

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
			} else if !sameFile(into.destination, destination) {
				return nil, fmt.Errorf("%s is drawn by two Texture Sets and would be written to both %s and %s; give them one name",
					stored.Relative, into.destination, destination)
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

	// Two files of one name would land on one file, which is refused before
	// anything is written.
	destinations := map[string]string{}
	claim := func(destination, what string) error {
		key := strings.ToLower(filepath.Clean(destination))
		if other, ok := destinations[key]; ok {
			return fmt.Errorf("%s and %s would both be written to %s; give them names of their own", other, what, destination)
		}

		destinations[key] = what

		return nil
	}

	for _, path := range order {
		if err := claim(targets[path].destination, targets[path].stored.Relative); err != nil {
			return nil, err
		}
	}

	plan := options.Plan
	if plan != nil && plan.Mesh != nil {
		if err := claim(plan.path(plan.Mesh.File), "the mesh"); err != nil {
			return nil, err
		}
	}

	if plan != nil && plan.Asset != nil {
		if err := claim(plan.path(plan.Asset.File), "the asset"); err != nil {
			return nil, err
		}
	}

	for _, path := range order {
		into := targets[path]

		// Written again only when it changes, since every write of a
		// compressed map loses a little: in what it holds, or how it is
		// stored, or, with a plan, which names every map it wants, where it
		// is.
		moved := plan != nil && !sameFile(into.destination, into.stored.Path)

		if into.original != nil && bytes.Equal(into.pixels.Pix, into.original.Pix) && into.format == into.stored.Format && !moved {
			packed.Unchanged = append(packed.Unchanged, into.stored.Relative)

			continue
		}

		stored := into.stored
		if into.format != stored.Format {
			stored.Format = into.format

			// A file of one level, as a PNG is, gets every level as a DDS.
			if stored.Levels <= 1 {
				stored.Levels = 99
			}
		}

		data, err := encode(into.pixels, stored)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", into.stored.Relative, err)
		}

		if !options.DryRun {
			if err := write(into.destination, data, options.Backup); err != nil {
				return nil, err
			}
		}

		packed.Written = append(packed.Written, into.destination)
	}

	if plan != nil {
		if err := plan.writeMeshAndAsset(manifest, options, packed); err != nil {
			return nil, err
		}
	}

	slices.Sort(packed.Unchanged)

	return packed, nil
}

// upgrade reads a manifest of the first version as one of now: its maps'
// formats named the way DXGI names them.
func (m *Manifest) upgrade() {
	for index := range m.TextureSets {
		for kind, stored := range m.TextureSets[index].Maps {
			stored.Format, stored.SRGB = currentFormat(stored.Format, stored.SRGB), false
			m.TextureSets[index].Maps[kind] = stored
		}
	}

	m.Version = Version
}

// where says where a map of a Texture Set is written, and in which format,
// or that it is not written at all, which only a plan leaves a map out by.
func (o PackOptions) where(set, kind string, stored Map) (destination, format string, wanted bool) {
	switch {
	case o.Plan != nil:
		planned, ok := o.Plan.mapOf(set, kind)
		if !ok {
			return "", "", false
		}

		format = stored.Format
		if planned.Format != "" {
			format = planned.Format
		}

		return o.Plan.path(planned.File), format, true

	case o.InPlace:
		return stored.Path, stored.Format, true
	}

	return filepath.Join(o.Out, filepath.Base(filepath.FromSlash(stored.Relative))), stored.Format, true
}

// sameFile reports whether two paths name one file, whatever their case, as
// they do on Windows.
func sameFile(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
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
