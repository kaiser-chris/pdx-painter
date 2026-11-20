package painter

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kaiser-chris/pdx-painter/internal/locate"
	"github.com/kaiser-chris/pdx-painter/internal/pdxmesh"
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

// PlannedMesh is the mesh file to write.
//
// A mesh the plugin opened is not changed by painting, so it is copied as it
// is: an asset names the textures, so the names inside the mesh do not matter
// to the games. A project the plugin did not open has no mesh file of the
// games behind it: its mesh is made from the OBJ Painter exports of it, its
// positions scaled by Scale, one shape for every Texture Set.
type PlannedMesh struct {
	File string `json:"file"`

	OBJ   string  `json:"obj,omitempty"`
	Scale float64 `json:"scale,omitempty"`
}

// PlannedAsset is the asset to write: its file, and the names of the pdxmesh
// and the entity it defines. MeshFile is the file its pdxmesh names where the
// plan writes no mesh and the manifest knows of none: one the user makes.
type PlannedAsset struct {
	File     string `json:"file"`
	Mesh     string `json:"mesh"`
	Entity   string `json:"entity"`
	MeshFile string `json:"meshFile,omitempty"`
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

	if p.Mesh != nil {
		if p.Mesh.OBJ == "" && manifest.MeshFile == nil {
			return fmt.Errorf("the mesh file of %s is not known, and no OBJ to make one of is given", manifest.Entity)
		}

		if p.Mesh.Scale < 0 {
			return fmt.Errorf("the mesh is to be scaled by %v, which is less than nothing", p.Mesh.Scale)
		}

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

		if p.Mesh == nil && manifest.MeshFile == nil {
			if err := name("the mesh the asset names", p.Asset.MeshFile, ".mesh"); err != nil {
				return err
			}
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

// writeMeshAndAsset copies or makes the mesh and writes the asset, as the plan
// says.
func (p *Plan) writeMeshAndAsset(manifest *Manifest, options PackOptions, packed *Packed) error {
	if p.Mesh != nil && p.Mesh.OBJ != "" {
		destination := p.path(p.Mesh.File)

		data, notes, err := p.meshOf(manifest)
		if err != nil {
			return err
		}

		packed.Notes = append(packed.Notes, notes...)

		if !options.DryRun {
			if err := write(destination, data, options.Backup); err != nil {
				return err
			}
		}

		packed.Written = append(packed.Written, destination)
	} else if p.Mesh != nil {
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
	var mesh string

	switch {
	case p.Mesh != nil:
		mesh = p.Mesh.File
	case manifest.MeshFile != nil:
		mesh = p.reference(manifest.MeshFile.Path, manifest.MeshFile.Relative, false)
	default:
		mesh = p.Asset.MeshFile
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

// meshOf makes the mesh of a project the plugin did not open, from the OBJ
// Painter exported of it: a shape for every material of the OBJ, which
// Painter names after the Texture Set it is, drawn with that set's shader and
// named after it, its material naming the maps the plan writes for the set.
// A material of no Texture Set is a shape of its own name, and noted.
func (p *Plan) meshOf(manifest *Manifest) ([]byte, []string, error) {
	file, err := os.Open(p.Mesh.OBJ)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	groups, err := pdxmesh.ReadOBJ(file)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", p.Mesh.OBJ, err)
	}

	if len(groups) == 0 {
		return nil, nil, fmt.Errorf("%s holds no faces", p.Mesh.OBJ)
	}

	scale := p.Mesh.Scale
	if scale == 0 {
		scale = 1
	}

	var (
		shapes []pdxmesh.Shape
		notes  []string
	)

	for _, group := range groups {
		set := manifest.set(group.Material)

		// A project of one Texture Set draws every face with it, whatever
		// the OBJ calls its material.
		if set == nil && len(manifest.TextureSets) == 1 {
			set = &manifest.TextureSets[0]
		}

		material := pdxmesh.Material{Shader: "standard"}
		shapeName := ShapeName(group.Material)

		if set != nil {
			shapeName = ShapeName(set.Name)

			if len(set.Meshes) > 0 {
				shapeName, material.Shader = set.Meshes[0].Shape, set.Meshes[0].Shader
			}

			material.Diffuse = p.fileOf(set.Name, Diffuse)
			material.Normal = p.fileOf(set.Name, Normal)
			material.Specular = p.fileOf(set.Name, Properties)
		} else {
			notes = append(notes, fmt.Sprintf("the mesh's material %s is no Texture Set; its shape names no textures", group.Material))
		}

		converted := pdxmesh.Convert(group, scale, material)

		at := -1
		for index := range shapes {
			if shapes[index].Name == shapeName {
				at = index
			}
		}

		if at < 0 {
			shapes = append(shapes, pdxmesh.Shape{Name: shapeName})
			at = len(shapes) - 1
		}

		shapes[at].Meshes = append(shapes[at].Meshes, converted)
	}

	data, err := pdxmesh.Write(shapes)

	return data, notes, err
}

// fileOf is the file name the plan writes a map of a set by, or none.
func (p *Plan) fileOf(set, kind string) string {
	if planned, ok := p.mapOf(set, kind); ok {
		return planned.File
	}

	return ""
}
