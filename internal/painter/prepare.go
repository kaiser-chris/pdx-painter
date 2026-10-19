package painter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kaiser-chris/pdx-asset-go/model"

	"github.com/kaiser-chris/pdx-painter/internal/locate"
)

// Options say how an entity is prepared.
type Options struct {
	// Attachments takes the entities attached to the entity as well, each
	// with its own Texture Sets. Without it only the entity's own parts are
	// taken, which are what its textures are for.
	Attachments bool

	// FlipGreen turns the y of the normal maps upside down for Painter, for
	// a project whose normals show the wrong way round.
	FlipGreen bool
}

// ManifestFile is the name of the manifest in the folder an entity is
// prepared in.
const ManifestFile = "manifest.json"

// Prepare writes what Painter needs to open an entity into a folder: the OBJ,
// a PNG for every channel and the manifest, which it returns as well.
func Prepare(opened *locate.Opened, entityName, folder string, options Options) (*Manifest, error) {
	built, diagnostics, err := opened.Loader.Load(entityName)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, err
	}

	manifest := &Manifest{
		Version:      Version,
		Asset:        opened.Location.File,
		Entity:       entityName,
		Root:         opened.Location.Root,
		Kind:         string(opened.Location.Kind),
		Product:      opened.Location.Product,
		NormalFormat: "DirectX",
		FlipGreen:    options.FlipGreen,
		Diagnostics:  []string{},
	}

	for _, diagnostic := range append(opened.Diagnostics, diagnostics...) {
		manifest.Diagnostics = append(manifest.Diagnostics, diagnostic.String())
	}

	groups := group(built, options.Attachments, manifest)
	if len(groups) == 0 {
		return nil, fmt.Errorf("entity %s draws no part with a texture to paint", entityName)
	}

	cache := map[string]*image.NRGBA{}

	for index := range groups {
		set, err := prepareSet(&groups[index], folder, options, cache)
		if err != nil {
			return nil, err
		}

		manifest.TextureSets = append(manifest.TextureSets, set)
	}

	manifest.Mesh = filepath.Join(folder, safeName(entityName)+".obj")
	if err := writeOBJ(manifest.Mesh, groups); err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	return manifest, os.WriteFile(filepath.Join(folder, ManifestFile), data, 0o644)
}

// partGroup is the parts of a model that are drawn with the same maps, which
// become one Texture Set.
type partGroup struct {
	name  string
	files model.TextureFiles
	uvSet int
	parts []*model.Part

	// planned are the maps the group is to have that are not there yet, by
	// kind, which packing makes.
	planned map[string]Map
}

// group gathers the parts that are painted into Texture Sets: those drawn
// with the same maps, laid by the same texture coordinates. A part with no
// map found is left out, since there is nothing of it to paint.
func group(built *model.Model, attachments bool, manifest *Manifest) []partGroup {
	var groups []partGroup

	taken := map[string]bool{}

	for index := range built.Parts {
		part := &built.Parts[index]
		files := part.Textures.Files

		if part.Attachment != 0 && !attachments || part.ShadowOnly {
			continue
		}

		if !files.Diffuse.Found() && !files.Normal.Found() && !files.Properties.Found() {
			if part.Shader != "" {
				manifest.Diagnostics = append(manifest.Diagnostics,
					fmt.Sprintf("note: part %s of %s has none of its maps, and is left out", part.Name, part.Entity))
			}

			continue
		}

		uvSet := 0
		if strings.Contains(strings.ToLower(part.Shader), "atlas") {
			uvSet = 1
		}

		at := slices.IndexFunc(groups, func(g partGroup) bool { return g.files == files && g.uvSet == uvSet })
		if at < 0 {
			name := uniqueName(setName(files.Diffuse.Relative, files.Properties.Relative, files.Normal.Relative), taken)
			taken[name] = true

			groups = append(groups, partGroup{name: name, files: files, uvSet: uvSet})
			at = len(groups) - 1
		}

		groups[at].parts = append(groups[at].parts, part)
	}

	return groups
}

// setName names a Texture Set after its diffuse map, or its other maps, the
// way the games name their textures: wood_diffuse.dds is wood.
// The names are paths of the maps, the first of them not empty taken.
func setName(names ...string) string {
	for _, relative := range names {
		if relative == "" {
			continue
		}

		name := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))

		for _, suffix := range []string{"_diffuse", "_normal", "_properties", "_specular", "_color", "_colour"} {
			if trimmed, ok := strings.CutSuffix(strings.ToLower(name), suffix); ok {
				name = name[:len(trimmed)]

				break
			}
		}

		return safeName(name)
	}

	return "material"
}

// safeName keeps a name to what Painter takes in a Texture Set's name.
func safeName(name string) string {
	var out strings.Builder

	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteRune('_')
		}
	}

	if out.Len() == 0 {
		return "material"
	}

	return out.String()
}

func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}

	for number := 2; ; number++ {
		if candidate := fmt.Sprintf("%s_%d", name, number); !taken[candidate] {
			return candidate
		}
	}
}

func prepareSet(g *partGroup, folder string, options Options, cache map[string]*image.NRGBA) (TextureSet, error) {
	set := TextureSet{Name: g.name, UVSet: g.uvSet, Maps: map[string]Map{}, Parts: []string{}, Shaders: []string{}, Channels: []Channel{}}

	for _, part := range g.parts {
		set.Parts = append(set.Parts, part.Name)

		if part.Shader != "" && !slices.Contains(set.Shaders, part.Shader) {
			set.Shaders = append(set.Shaders, part.Shader)
		}
	}

	pixels := map[string]*image.NRGBA{}

	maps := []struct {
		kind string
		file model.TextureFile
	}{{Diffuse, g.files.Diffuse}, {Normal, g.files.Normal}, {Properties, g.files.Properties}}

	for _, m := range maps {
		kind, file := m.kind, m.file
		if !file.Found() {
			if planned, ok := g.planned[kind]; ok {
				set.Maps[kind] = planned
			}

			continue
		}

		stored, err := storage(file.Path)
		if err != nil {
			return set, err
		}

		img, ok := cache[file.Path]
		if !ok {
			if img, err = readPixels(file.Path); err != nil {
				return set, err
			}

			cache[file.Path] = img
		}

		stored.Path, stored.Relative, stored.Source = file.Path, file.Relative, file.Source
		stored.Width, stored.Height = img.Rect.Dx(), img.Rect.Dy()
		set.Maps[kind] = stored
		pixels[kind] = img

		if stored.Width*stored.Height > set.Width*set.Height {
			set.Width, set.Height = stored.Width, stored.Height
		}
	}

	// A set of nothing but new maps is painted at the size most of the
	// games' maps are.
	if set.Width == 0 {
		set.Width, set.Height = newSize, newSize
	}

	channels := filepath.Join(folder, set.Name)
	if err := os.MkdirAll(channels, 0o755); err != nil {
		return set, err
	}

	set.Coverage = filepath.Join(channels, "coverage.png")
	if err := writePNG(set.Coverage, coverage(g, set.Width, set.Height)); err != nil {
		return set, err
	}

	for _, s := range specs {
		stored, ok := set.Maps[s.mapName]
		if !ok {
			continue
		}

		img := pixels[s.mapName]

		// The masks only some shaders read are made only where a map has
		// them already.
		if s.optional && (stored.New || !varies(img, component(s.part))) {
			continue
		}

		channel := Channel{
			ID: s.id, Map: s.mapName, Painter: s.painter, Format: s.format, Label: s.label,
			ExportType: s.exportType, ExportName: s.exportName,
			Color: s.part == "rgb" || s.part == "normal",
		}

		if stored.New {
			if start, ok := starts[s.id]; ok {
				channel.Default = &start
			}
		} else {
			channel.Image = filepath.Join(channels, s.id+".png")

			if err := writePNG(channel.Image, s.extract(img, options.FlipGreen)); err != nil {
				return set, err
			}
		}

		set.Channels = append(set.Channels, channel)
	}

	return set, nil
}

// writeOBJ writes the parts as one OBJ, a material for every Texture Set,
// which Painter makes a Texture Set of.
//
// The model's coordinates are already the right handed ones OBJ has, with
// the front faces wound anticlockwise. OBJ counts its texture coordinates up
// from the bottom, where the games count them down from the top.
func writeOBJ(path string, groups []partGroup) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	out := bufio.NewWriterSize(file, 1<<20)
	number := func(value float32) {
		out.Write(strconv.AppendFloat(nil, float64(value), 'g', -1, 32))
	}

	fmt.Fprintf(out, "# Written by pdx-painter\nmtllib %s\n", strings.TrimSuffix(filepath.Base(path), ".obj")+".mtl")

	base := 1

	for _, g := range groups {
		fmt.Fprintf(out, "o %s\nusemtl %s\n", g.name, g.name)

		for _, part := range g.parts {
			for index := range part.Pieces {
				piece := &part.Pieces[index]
				uv := piece.UV0
				if g.uvSet == 1 {
					uv = piece.UV1
				}

				vertices := piece.Vertices()

				for v := range vertices {
					out.WriteString("v ")
					number(piece.Positions[v*3])
					out.WriteByte(' ')
					number(piece.Positions[v*3+1])
					out.WriteByte(' ')
					number(piece.Positions[v*3+2])
					out.WriteString("\nvt ")
					number(uv[v*2])
					out.WriteByte(' ')
					number(1 - uv[v*2+1])
					out.WriteString("\nvn ")
					number(piece.Normals[v*3])
					out.WriteByte(' ')
					number(piece.Normals[v*3+1])
					out.WriteByte(' ')
					number(piece.Normals[v*3+2])
					out.WriteByte('\n')
				}

				for t := 0; t+2 < len(piece.Indices); t += 3 {
					out.WriteString("f")

					for corner := range 3 {
						index := strconv.Itoa(base + int(piece.Indices[t+corner]))
						out.WriteString(" " + index + "/" + index + "/" + index)
					}

					out.WriteByte('\n')
				}

				base += vertices
			}
		}
	}

	if err := out.Flush(); err != nil {
		return err
	}

	var materials strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&materials, "newmtl %s\n", g.name)
	}

	return os.WriteFile(strings.TrimSuffix(path, ".obj")+".mtl", []byte(materials.String()), 0o644)
}
