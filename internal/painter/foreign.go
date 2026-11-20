package painter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kaiser-chris/pdx-painter/internal/pdxmesh"
)

// KindForeign is the Kind of a manifest of a Painter project the plugin did
// not open: one made from a model of the user's own, which has no game files
// behind it yet.
const KindForeign = "foreign"

// PrepareForeign writes the manifest of a Painter project the plugin did not
// open, from the names of its Texture Sets, so that it can be exported to the
// games as one it opened: every map of every set is a new map, made from the
// channels Painter exports, and the mesh is made from the OBJ Painter exports
// of the project, see Plan.
//
// name names the project, which the files are named after unless the export
// names them otherwise. A set's shape in the mesh is named after the set, as
// far as the games' shape names go.
func PrepareForeign(name string, sets []string, folder string) (*Manifest, error) {
	if len(sets) == 0 {
		return nil, fmt.Errorf("the project has no Texture Set")
	}

	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, err
	}

	manifest := &Manifest{
		Version:      Version,
		Entity:       name,
		Kind:         KindForeign,
		NormalFormat: "DirectX",
		Diagnostics:  []string{},
	}

	seen := map[string]bool{}

	for _, setName := range sets {
		if seen[setName] {
			return nil, fmt.Errorf("the project has two Texture Sets of the name %s", setName)
		}

		seen[setName] = true

		set := TextureSet{
			Name: setName, Width: newSize, Height: newSize,
			Parts: []string{}, Shaders: []string{"standard"}, Channels: []Channel{},
			Maps: map[string]Map{},
			Meshes: []MeshRef{{
				Shape: ShapeName(setName), Shader: "standard", ShaderFile: shaderFileOf("standard"),
			}},
		}

		for _, kind := range []string{Diffuse, Normal, Properties} {
			file := setName + "_" + kind + ".dds"
			set.Maps[kind] = Map{Path: filepath.Join(folder, file), Relative: file, Source: KindForeign, New: true, Format: newFormat, Levels: 99}
		}

		for _, s := range specs {
			if s.optional {
				continue
			}

			channel := Channel{
				ID: s.id, Map: s.mapName, Painter: s.painter, Format: s.format, Label: s.label,
				ExportType: s.exportType, ExportName: s.exportName,
				Color: s.part == "rgb" || s.part == "normal",
			}

			if start, ok := starts[s.id]; ok {
				channel.Default = &start
			}

			set.Channels = append(set.Channels, channel)
		}

		manifest.TextureSets = append(manifest.TextureSets, set)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	return manifest, os.WriteFile(filepath.Join(folder, ManifestFile), data, 0o644)
}

// ShapeName is the name a Texture Set's shape has in a mesh written for it:
// the set's, cut to the length the games' shape names go to.
func ShapeName(set string) string {
	if len(set) > pdxmesh.MaxNameLength {
		return set[:pdxmesh.MaxNameLength]
	}

	return set
}
