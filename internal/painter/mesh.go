package painter

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaiser-chris/pdx-asset-go/mesh"
	"github.com/kaiser-chris/pdx-asset-go/model"
)

// newSize is the size a Texture Set of nothing but new maps is painted at.
const newSize = 1024

// KindMesh is the Kind of a manifest prepared from a mesh file of its own,
// with no asset file around it.
const KindMesh = "mesh"

// PrepareMesh writes what Painter needs to paint a mesh file of its own into
// a folder: for a mesh that has no textures yet, or one kept outside a game.
//
// A mesh file names the textures of each of its meshes in its material, as
// the exporters of the games write it. Those found next to the mesh file are
// read the way an entity's are; those that are not there, and those the
// material names none for, are new maps, which packing makes next to the
// mesh. A map the material names none for is named after the mesh, and its
// shape where the mesh has several: house_diffuse.dds.
func PrepareMesh(meshPath, folder string, options Options) (*Manifest, error) {
	absolute, err := filepath.Abs(meshPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}

	read, err := mesh.Read(data)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", absolute, err)
	}

	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, err
	}

	base := strings.TrimSuffix(filepath.Base(absolute), filepath.Ext(absolute))

	manifest := &Manifest{
		Version:      Version,
		Asset:        absolute,
		Entity:       base,
		Root:         filepath.Dir(absolute),
		Kind:         KindMesh,
		NormalFormat: "DirectX",
		FlipGreen:    options.FlipGreen,
		Diagnostics:  []string{},
	}

	for _, warning := range read.Warnings {
		manifest.Diagnostics = append(manifest.Diagnostics, "warning: "+absolute+": "+warning)
	}

	groups := meshGroups(read, filepath.Dir(absolute), base)
	if len(groups) == 0 {
		return nil, fmt.Errorf("%s holds no geometry to paint", absolute)
	}

	cache := map[string]*image.NRGBA{}

	for index := range groups {
		set, err := prepareSet(&groups[index], folder, options, cache)
		if err != nil {
			return nil, err
		}

		manifest.TextureSets = append(manifest.TextureSets, set)
	}

	manifest.Mesh = filepath.Join(folder, safeName(base)+".obj")
	if err := writeOBJ(manifest.Mesh, groups); err != nil {
		return nil, err
	}

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	return manifest, os.WriteFile(filepath.Join(folder, ManifestFile), encoded, 0o644)
}

// meshGroups gathers the most detailed meshes of a mesh file into Texture
// Sets: those whose materials name the same textures, which a mesh without
// any names shares with nothing.
func meshGroups(read *mesh.File, folder, base string) []partGroup {
	lowest := -1
	for _, shape := range read.Shapes {
		if len(shape.Meshes) > 0 && (lowest < 0 || shape.LOD < lowest) {
			lowest = shape.LOD
		}
	}

	type key struct{ diffuse, normal, specular, shape string }

	var (
		groups []partGroup
		keys   []key
	)

	taken := map[string]bool{}
	shapes := 0

	for _, shape := range read.Shapes {
		if shape.LOD == lowest && len(shape.Meshes) > 0 {
			shapes++
		}
	}

	for _, shape := range read.Shapes {
		if shape.LOD != lowest {
			continue
		}

		for index := range shape.Meshes {
			source := &shape.Meshes[index]
			material := source.Material

			// A mesh whose material names no texture is painted on its own.
			k := key{diffuse: material.Diffuse, normal: material.Normal, specular: material.Specular}
			if k == (key{}) {
				k.shape = fmt.Sprintf("%s#%d", shape.Name, index)
			}

			part := &model.Part{Name: shape.Name, Shader: material.Shader, Pieces: model.Convert(source)}

			at := -1
			for position, existing := range keys {
				if existing == k {
					at = position
				}
			}

			if at < 0 {
				// One set of a mesh is named after it, several after their
				// shapes as well.
				stem := base
				if shapes > 1 {
					stem = base + "_" + shapeStem(shape.Name)
				}

				g := partGroup{planned: map[string]Map{}}
				g.files.Diffuse = nextTo(folder, material.Diffuse)
				g.files.Normal = nextTo(folder, material.Normal)
				g.files.Properties = nextTo(folder, material.Specular)

				for kind, named := range map[string]string{Diffuse: material.Diffuse, Normal: material.Normal, Properties: material.Specular} {
					file := map[string]model.TextureFile{Diffuse: g.files.Diffuse, Normal: g.files.Normal, Properties: g.files.Properties}[kind]
					if !file.Found() {
						g.planned[kind] = planned(folder, named, stem, kind)
					}
				}

				g.name = uniqueName(setName(
					g.files.Diffuse.Relative+g.planned[Diffuse].Relative,
					g.files.Properties.Relative+g.planned[Properties].Relative,
					g.files.Normal.Relative+g.planned[Normal].Relative,
				), taken)
				taken[g.name] = true

				groups = append(groups, g)
				keys = append(keys, k)
				at = len(groups) - 1
			}

			groups[at].parts = append(groups[at].parts, part)
		}
	}

	return groups
}

// nextTo finds a texture a material names next to the mesh file, by its file
// name, the way files taken out of a game are kept together.
func nextTo(folder, named string) model.TextureFile {
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(named, "\\", "/")))
	if named == "" || name == "." {
		return model.TextureFile{}
	}

	path := filepath.Join(folder, name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return model.TextureFile{}
	}

	return model.TextureFile{Path: path, Relative: name, Source: "mesh"}
}

// planned is a new map next to the mesh file: by the name the material gives
// it, or the mesh's name and the map's kind.
func planned(folder, named, stem, kind string) Map {
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(named, "\\", "/")))
	if named == "" || name == "." {
		name = stem + "_" + kind + ".dds"
	}

	// The tool writes DDS, PNG and TGA; a name of anything else is written
	// as DDS.
	switch strings.ToLower(filepath.Ext(name)) {
	case ".dds", ".png", ".tga":
	default:
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".dds"
	}

	stored := Map{Path: filepath.Join(folder, name), Relative: name, Source: "mesh", New: true, Format: newFormat, Levels: 99}

	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		stored.Format, stored.Levels = "PNG", 1
	case ".tga":
		stored.Format, stored.Levels = "TGA", 1
	}

	return stored
}

// shapeStem is what of a shape's name goes into a file name: the games'
// exporters name shapes after their Maya nodes, such as house_roofShape or
// LOD_0|house|house_roof.
func shapeStem(name string) string {
	if at := strings.LastIndexAny(name, "|:"); at >= 0 {
		name = name[at+1:]
	}

	name = strings.TrimSuffix(name, "Shape")

	return safeName(strings.ToLower(name))
}
