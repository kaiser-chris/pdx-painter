package painter

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
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
// The maps are named after the mesh file, house_diffuse.dds and so on, see
// meshGroups. Those found next to the mesh are read the way an entity's are;
// the rest are new maps, which packing makes next to the mesh.
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
		MeshFile:     &SourceFile{Path: absolute, Relative: filepath.Base(absolute)},
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
// Sets: one for the whole mesh, or one for each shape where it has several.
//
// The names of the textures the mesh's materials give are left alone: they
// are what the mesh was exported with, which the asset files that draw it
// often name otherwise. The maps are named after the mesh file instead,
// house_diffuse.dds, the sets after the first numbered in the order of their
// shapes, house_2_diffuse.dds. The names of the shapes are no help: the
// exporters cut them off at a length. Those that are next to the mesh already are read;
// the rest are new maps, which packing makes there.
func meshGroups(read *mesh.File, folder, base string) []partGroup {
	lowest := -1
	for _, shape := range read.Shapes {
		if len(shape.Meshes) > 0 && (lowest < 0 || shape.LOD < lowest) {
			lowest = shape.LOD
		}
	}

	shapes := 0
	for _, shape := range read.Shapes {
		if shape.LOD == lowest && len(shape.Meshes) > 0 && !collision(shape) {
			shapes++
		}
	}

	var (
		groups []partGroup
		keys   []string
	)

	taken := map[string]bool{}

	for _, shape := range read.Shapes {
		if shape.LOD != lowest || len(shape.Meshes) == 0 || collision(shape) {
			continue
		}

		// A mesh of one shape is one set; several shapes are a set each.
		key := ""
		if shapes > 1 {
			key = shape.Name
		}

		at := slices.Index(keys, key)
		if at < 0 {
			stem := base
			if len(groups) > 0 {
				stem = fmt.Sprintf("%s_%d", base, len(groups)+1)
			}

			g := partGroup{planned: map[string]Map{}}

			for _, kind := range []string{Diffuse, Normal, Properties} {
				file := nextTo(folder, stem+"_"+kind+".dds")
				if !file.Found() {
					g.planned[kind] = planned(folder, stem+"_"+kind+".dds")
				}

				switch kind {
				case Diffuse:
					g.files.Diffuse = file
				case Normal:
					g.files.Normal = file
				case Properties:
					g.files.Properties = file
				}
			}

			g.name = uniqueName(safeName(stem), taken)
			taken[g.name] = true

			groups = append(groups, g)
			keys = append(keys, key)
			at = len(groups) - 1
		}

		for index := range shape.Meshes {
			source := &shape.Meshes[index]
			part := &model.Part{Name: shape.Name, Shader: source.Material.Shader, Pieces: model.Convert(source)}
			groups[at].parts = append(groups[at].parts, part)
			groups[at].meshes = append(groups[at].meshes, MeshRef{
				Shape: shape.Name, Index: index, Shader: source.Material.Shader, ShaderFile: shaderFileOf(source.Material.Shader),
			})
		}
	}

	return groups
}

// nextTo finds a map of a name next to the mesh file.
func nextTo(folder, name string) model.TextureFile {
	path := filepath.Join(folder, name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return model.TextureFile{}
	}

	return model.TextureFile{Path: path, Relative: name, Source: "mesh"}
}

// planned is a new map of a name next to the mesh file.
func planned(folder, name string) Map {
	return Map{Path: filepath.Join(folder, name), Relative: name, Source: "mesh", New: true, Format: newFormat, Levels: 99}
}

// collision reports whether a shape is there for the engine to collide with
// rather than to be seen, which the exporters give the shader Collision, and
// which has nothing to paint.
func collision(shape mesh.Shape) bool {
	for _, source := range shape.Meshes {
		if !strings.EqualFold(source.Material.Shader, "collision") {
			return false
		}
	}

	return true
}
