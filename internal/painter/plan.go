package painter

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kaiser-chris/pdx-painter/internal/locate"
)

// Plan is what an advanced export writes, into one folder: the maps chosen,
// each by a name and in a format of its own, and a copy of the mesh and an
// asset drawing it with them. It is how something opened and changed is saved
// as a new thing, without touching the files it came from.
type Plan struct {
	Folder string `json:"folder"`

	Maps  []PlannedMap  `json:"maps"`
	Mesh  *PlannedMesh  `json:"mesh,omitempty"`
	Asset *PlannedAsset `json:"asset,omitempty"`
}

// PlannedMap is one map of a Texture Set to write: its kind, the file name to
// write it by, and its format, empty for the one it was read in.
type PlannedMap struct {
	Set    string `json:"set"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Format string `json:"format,omitempty"`
}

// PlannedMesh is the copy of the mesh file to write. The mesh is not changed
// by painting, so it is copied as it is: an asset names the textures, so the
// names inside the mesh do not matter to the games.
type PlannedMesh struct {
	File string `json:"file"`
}

// PlannedAsset is the asset to write: its file, and the names of the pdxmesh
// and the entity it defines.
type PlannedAsset struct {
	File   string `json:"file"`
	Mesh   string `json:"mesh"`
	Entity string `json:"entity"`
}

// check refuses a plan that cannot be carried out, before anything is
// written.
func (p *Plan) check(manifest *Manifest) error {
	if p.Folder == "" {
		return fmt.Errorf("the plan names no folder to write into")
	}

	name := func(what, file, extension string) error {
		if file == "" || file != filepath.Base(file) || file == "." || file == ".." {
			return fmt.Errorf("%s is to be written as %q, which is not a file name", what, file)
		}

		if extension != "" && !strings.EqualFold(filepath.Ext(file), extension) {
			return fmt.Errorf("%s is to be written as %s, which does not end in %s", what, file, extension)
		}

		return nil
	}

	for _, planned := range p.Maps {
		set := manifest.set(planned.Set)
		if set == nil {
			return fmt.Errorf("the plan names a Texture Set %s the manifest does not have", planned.Set)
		}

		stored, ok := set.Maps[planned.Kind]
		if !ok {
			return fmt.Errorf("Texture Set %s has no %s map", planned.Set, planned.Kind)
		}

		if planned.Format != "" && (!writable(planned.Format) || planned.Format == Legacy) {
			return fmt.Errorf("%s of %s is to be written as %s, which is not a format this tool writes", planned.Kind, planned.Set, planned.Format)
		}

		format := planned.Format
		if format == "" {
			format = stored.Format
		}

		if err := name(planned.Kind+" of "+planned.Set, planned.File, extensionOf(format)); err != nil {
			return err
		}
	}

	if (p.Mesh != nil || p.Asset != nil) && manifest.MeshFile == nil {
		return fmt.Errorf("the mesh file of %s is not known, so neither it nor an asset can be written", manifest.Entity)
	}

	if p.Mesh != nil {
		if err := name("the mesh", p.Mesh.File, ".mesh"); err != nil {
			return err
		}
	}

	if p.Asset != nil {
		if err := name("the asset", p.Asset.File, ".asset"); err != nil {
			return err
		}

		if p.Asset.Mesh == "" || p.Asset.Entity == "" {
			return fmt.Errorf("the asset needs a name for its pdxmesh and its entity")
		}
	}

	return nil
}

// extensionOf is the file extension a format is written with.
func extensionOf(format string) string {
	switch format {
	case "PNG":
		return ".png"
	case "TGA":
		return ".tga"
	}

	return ".dds"
}

func (m *Manifest) set(name string) *TextureSet {
	for index := range m.TextureSets {
		if m.TextureSets[index].Name == name {
			return &m.TextureSets[index]
		}
	}

	return nil
}

func (p *Plan) mapOf(set, kind string) (PlannedMap, bool) {
	for _, planned := range p.Maps {
		if planned.Set == set && planned.Kind == kind {
			return planned, true
		}
	}

	return PlannedMap{}, false
}

func (p *Plan) path(file string) string {
	return filepath.Join(p.Folder, file)
}

// writeMeshAndAsset copies the mesh and writes the asset, as the plan says.
func (p *Plan) writeMeshAndAsset(manifest *Manifest, options PackOptions, packed *Packed) error {
	if p.Mesh != nil {
		destination := p.path(p.Mesh.File)

		if sameFile(destination, manifest.MeshFile.Path) {
			packed.Notes = append(packed.Notes, "the mesh is already "+destination)
		} else {
			data, err := os.ReadFile(manifest.MeshFile.Path)
			if err != nil {
				return err
			}

			if !options.DryRun {
				if err := write(destination, data, options.Backup); err != nil {
					return err
				}
			}

			packed.Written = append(packed.Written, destination)
		}
	}

	if p.Asset != nil {
		destination := p.path(p.Asset.File)

		if !options.DryRun {
			if err := write(destination, []byte(p.assetText(manifest)), options.Backup); err != nil {
				return err
			}
		}

		packed.Written = append(packed.Written, destination)
	}

	return nil
}

// textureKeys are the keys of the mesh settings that name each kind of map.
var textureKeys = []struct{ kind, key string }{
	{Diffuse, "texture_diffuse"},
	{Normal, "texture_normal"},
	{Properties, "texture_specular"},
}

// assetText is the asset the plan writes: a pdxmesh naming the mesh, with mesh
// settings for every mesh of it that is painted, and an entity drawing it.
//
// The files are named the way the games' asset files name them, relative to
// the folder of the asset. The ones the plan writes are next to it, by their
// names; the mesh and the maps it does not write are the ones they were read
// from, see reference.
func (p *Plan) assetText(manifest *Manifest) string {
	mesh := p.reference(manifest.MeshFile.Path, manifest.MeshFile.Relative, false)
	if p.Mesh != nil {
		mesh = p.Mesh.File
	}

	var text strings.Builder

	fmt.Fprintf(&text, "pdxmesh = {\n\tname = %q\n\tfile = %q\n", p.Asset.Mesh, mesh)

	for _, set := range manifest.TextureSets {
		for _, ref := range set.Meshes {
			fmt.Fprintf(&text, "\n\tmeshsettings = {\n\t\tname = %q\n\t\tindex = %d\n", ref.Shape, ref.Index)

			for _, texture := range textureKeys {
				if file := p.textureOf(&set, texture.kind); file != "" {
					fmt.Fprintf(&text, "\t\t%s = %q\n", texture.key, file)
				}
			}

			shader := ref.Shader
			if shader == "" {
				shader = "standard"
			}

			fmt.Fprintf(&text, "\t\tshader = %q\n", shader)

			shaderFile := ref.ShaderFile
			if shaderFile == "" {
				shaderFile = shaderFileOf(shader)
			}

			fmt.Fprintf(&text, "\t\tshader_file = %q\n", shaderFile)

			if len(ref.ShaderDefines) > 0 {
				quoted := make([]string, len(ref.ShaderDefines))
				for index, define := range ref.ShaderDefines {
					quoted[index] = fmt.Sprintf("%q", define)
				}

				fmt.Fprintf(&text, "\t\tadditional_shader_defines = { %s }\n", strings.Join(quoted, " "))
			}

			if ref.Subpass != "" {
				fmt.Fprintf(&text, "\t\tsubpass = %q\n", ref.Subpass)
			}

			if ref.ShadowOnly {
				text.WriteString("\t\tshadow_only = yes\n")
			}

			text.WriteString("\t}\n")
		}
	}

	fmt.Fprintf(&text, "}\n\nentity = {\n\tname = %q\n\tpdxmesh = %q\n}\n", p.Asset.Entity, p.Asset.Mesh)

	return text.String()
}

// textureOf is the file the asset names for a map of a Texture Set: the one
// the plan writes, or the one it was read from, or none for a new map that is
// not written, since there is no such file.
func (p *Plan) textureOf(set *TextureSet, kind string) string {
	if planned, ok := p.mapOf(set.Name, kind); ok {
		return planned.File
	}

	stored, ok := set.Maps[kind]
	if !ok || stored.New {
		return ""
	}

	return p.reference(stored.Path, stored.Relative, true)
}

// reference is how the asset names a file it does not write, relative to the
// folder the asset is in, the way the games resolve the files their assets
// name.
//
// A texture below gfx/models is named by its file name alone: the engine
// finds those by name wherever they are below that folder, in the game or a
// mod. Anything else is named by its path from the asset's folder: below the
// root of the game or mod, where the asset is in one and the file was found
// below a root of its own, so that a mod's asset names a file of the game by
// where it is in every folder; or between the two folders on disk.
func (p *Plan) reference(absolute, relative string, texture bool) string {
	relative = filepath.ToSlash(relative)

	if texture && (strings.HasPrefix(relative, "gfx/models/") || strings.Contains(relative, "/gfx/models/")) {
		return path.Base(relative)
	}

	if _, folder, ok := locate.Below(p.Folder); ok && strings.Contains(relative, "/") {
		if between, err := filepath.Rel(filepath.FromSlash(folder), filepath.FromSlash(relative)); err == nil {
			return filepath.ToSlash(between)
		}
	}

	if between, err := filepath.Rel(p.Folder, absolute); err == nil {
		return filepath.ToSlash(between)
	}

	return filepath.ToSlash(absolute)
}
