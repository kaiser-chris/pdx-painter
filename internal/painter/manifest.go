// Package painter turns an entity of the Paradox games into what Substance 3D
// Painter opens, and what Painter exports back into the games' textures.
//
// Preparing an entity writes a mesh Painter creates a project from, one PNG
// for every channel of every map, and a manifest that says which Painter
// channel each PNG goes into and where every map came from. Packing reads the
// PNGs Painter exports, lays each into the channels of the map it came from,
// keeps whatever channel Painter was not given from the original, and writes
// the maps back in the format and with the mipmaps they were read with.
//
// # Channels
//
// The games pack several values into each map, the way their shaders read
// them (see pdx-asset-go's render/shaders/standard.fs):
//
//   - the diffuse map, the colour in RGB and in alpha a mask whose meaning
//     depends on the shader: the palette colour for skin, a cut out for hair
//     and leaves;
//   - the normal map, the x of the normal in green and its y in alpha, with
//     other masks in red and blue for a few shaders, such as hair;
//   - the properties map, a mask of subsurface scattering in red, the
//     strength of the specular light in green, metalness in blue and
//     roughness in alpha.
//
// Painter's own channels take what it can show: base colour, normal,
// metallic and roughness. The rest go into user channels, labelled with what
// they are.
package painter

import (
	"image"
	"math"
	"strings"
)

// Version is the version of the manifest, which a plugin checks before it
// reads one.
const Version = 2

// FirstVersion is the version of the first manifests, which named the DDS
// formats otherwise and are read still.
const FirstVersion = 1

// Manifest is what preparing an entity wrote, and everything packing needs to
// write the textures back.
type Manifest struct {
	Version int `json:"version"`

	// Asset is the asset file, Entity the entity of it prepared, and Root
	// and Kind the root of its game, mod or loose folder and which it is.
	Asset  string `json:"asset"`
	Entity string `json:"entity"`
	Root   string `json:"root"`
	Kind   string `json:"kind"`

	// Game is the game folder a mod was read with.
	Game string `json:"game,omitempty"`

	// Product is the game the files are of, such as victoria3 or eu5, where
	// that is known.
	Product string `json:"product,omitempty"`

	// MeshFile is the .mesh file the entity draws, or the mesh opened on its
	// own, which an export as a new asset copies. It is nil for an entity
	// whose mesh file is not known.
	MeshFile *SourceFile `json:"meshFile,omitempty"`

	// Mesh is the OBJ file to create the Painter project from.
	Mesh string `json:"mesh"`

	// NormalFormat is the normal map format the Painter project is to be
	// created with, which the PNGs are written in, and FlipGreen says the
	// y of the normals was turned upside down on the way, as it is on the
	// way back.
	NormalFormat string `json:"normalFormat"`
	FlipGreen    bool   `json:"flipGreen"`

	TextureSets []TextureSet `json:"textureSets"`

	Diagnostics []string `json:"diagnostics"`
}

// TextureSet is one Texture Set of the Painter project: a material of the
// OBJ, made of the parts that are drawn with the same maps.
type TextureSet struct {
	Name string `json:"name"`

	// Width and Height are the size of the largest of its maps, which the
	// Texture Set is given, as near as Painter's powers of two come.
	Width  int `json:"width"`
	Height int `json:"height"`

	// Parts are the shapes of the mesh drawn with these maps, and Shaders
	// the effects they are drawn with.
	Parts   []string `json:"parts"`
	Shaders []string `json:"shaders"`

	// UVSet is the set of texture coordinates the maps are laid by: 1 for
	// the atlases of standard_atlas, 0 otherwise.
	UVSet int `json:"uvSet"`

	// Maps are the maps the parts are drawn with, by kind: diffuse, normal
	// and properties.
	Maps map[string]Map `json:"maps"`

	Channels []Channel `json:"channels"`

	// Meshes are the meshes of MeshFile the set's maps are laid on, which an
	// asset written for it gives mesh settings. A part of an entity attached
	// is drawn from another mesh file and is not among them.
	Meshes []MeshRef `json:"meshes"`

	// Coverage is a mask of the texels the parts cover in texture space, at
	// the Texture Set's size: what of Painter's export is laid into the
	// maps, which other models may share. See coverage.go.
	Coverage string `json:"coverage"`
}

// SourceFile is a file the manifest was prepared from: the file itself, and
// its path below the root it was found in, with forward slashes.
type SourceFile struct {
	Path     string `json:"path"`
	Relative string `json:"relative"`
}

// MeshRef is one mesh of a mesh file: the shape it is of, its place among the
// shape's meshes, and how the game draws it, which is what the mesh settings
// of an asset name. For an entity, how it is drawn is its own mesh settings';
// for a mesh opened on its own, its material's shader, and the shader file
// the games ship that shader in.
type MeshRef struct {
	Shape  string `json:"shape"`
	Index  int    `json:"index"`
	Shader string `json:"shader"`

	ShaderFile    string   `json:"shaderFile,omitempty"`
	ShaderDefines []string `json:"shaderDefines,omitempty"`
	Subpass       string   `json:"subpass,omitempty"`
	ShadowOnly    bool     `json:"shadowOnly,omitempty"`
}

// shaderFiles are the shader files the games ship their mesh shaders in, by
// what the name of the shader starts with or holds, the first that fits
// taken: what nearly every mesh setting of Victoria 3 and Europa Universalis
// 5 names. The rest are pdxmesh.shader's.
var shaderFiles = []struct{ holds, file string }{
	{"decal", "gfx/FX/pdxmesh_decal.shader"},
	{"portrait", "gfx/FX/jomini/portrait.shader"},
	{"tree", "gfx/FX/tree.shader"},
	{"lake", "gfx/FX/pdxwater.shader"},
}

// shaderFileOf is the shader file a shader is shipped in, as shaderFiles say.
func shaderFileOf(shader string) string {
	lower := strings.ToLower(shader)

	for _, candidate := range shaderFiles {
		if strings.Contains(lower, candidate.holds) {
			return candidate.file
		}
	}

	return "gfx/FX/pdxmesh.shader"
}

// Map is a texture file of the game, and how it is stored.
type Map struct {
	// Path is the file, Relative its path below the root of the folder it
	// was found in, and Source the name of that folder.
	Path     string `json:"path"`
	Relative string `json:"relative"`
	Source   string `json:"source"`

	Width  int `json:"width"`
	Height int `json:"height"`

	// Format is how the file stores its pixels: a DDS format by the name DXGI
	// gives it, such as BC3_UNORM, Legacy for a DDS file of a layout DXGI has
	// no format for, or PNG or TGA. Levels is how many mipmap levels it has.
	// See ddsfile.go.
	Format string `json:"format"`
	Levels int    `json:"levels"`

	// SRGB is what the first manifests said apart from the format: whether
	// it is sRGB. A manifest of now names the sRGB formats, such as
	// BC7_UNORM_SRGB, and leaves it out.
	SRGB bool `json:"srgb,omitempty"`

	// New is set for a map that is not there yet, which packing makes: one a
	// mesh names that is not next to it, or that it names none for. Path is
	// where it is written, and its size is that of what Painter exports.
	New bool `json:"new,omitempty"`
}

// Channel is one channel of a Texture Set, which one PNG fills.
type Channel struct {
	// ID names the channel, and the file Painter exports it to, which is
	// the Texture Set's name and the ID: wood_basecolor.png.
	ID string `json:"id"`

	// Map is the map the channel comes from, and Painter the Painter
	// channel it goes into, by the name of its ChannelType, with its
	// ChannelFormat and, for a user channel, a label.
	Map     string `json:"map"`
	Painter string `json:"painter"`
	Format  string `json:"format"`
	Label   string `json:"label,omitempty"`

	// Image is the PNG to fill the channel from.
	Image string `json:"image"`

	// ExportType and ExportName are the source of the channel in a Painter
	// export preset, such as documentMap and basecolor, and Color whether it
	// is exported in colour, as R, G and B, rather than as L.
	ExportType string `json:"exportType"`
	ExportName string `json:"exportName"`
	Color      bool   `json:"color"`

	// Default is the value a channel of a new map starts at, the same in red,
	// green and blue, from 0 to 1. A channel with no Image and no Default
	// starts at what Painter starts it at.
	Default *float64 `json:"default,omitempty"`
}

// Kinds of map.
const (
	Diffuse    = "diffuse"
	Normal     = "normal"
	Properties = "properties"
)

// spec is how one channel of a map goes into Painter and back.
type spec struct {
	id, mapName string

	// part is which of the map's channels the Painter channel holds: one of
	// R, G, B and A, or rgb for the colour, or normal for the x and y of a
	// normal map.
	part string

	painter, format, label string
	exportType, exportName string

	// optional channels are left out where the map holds one value
	// throughout, which most do: only a few shaders read them.
	optional bool
}

var specs = []spec{
	{id: "basecolor", mapName: Diffuse, part: "rgb", painter: "BaseColor", format: "sRGB8", exportType: "documentMap", exportName: "basecolor"},
	{id: "diffusealpha", mapName: Diffuse, part: "A", painter: "User0", format: "L8", label: "Diffuse alpha", exportType: "documentMap", exportName: "user0"},
	{id: "normal", mapName: Normal, part: "normal", painter: "Normal", format: "RGB8", exportType: "virtualMap", exportName: "Normal_DirectX"},
	{id: "normalred", mapName: Normal, part: "R", painter: "User3", format: "L8", label: "Normal map red", exportType: "documentMap", exportName: "user3", optional: true},
	{id: "normalblue", mapName: Normal, part: "B", painter: "User4", format: "L8", label: "Normal map blue", exportType: "documentMap", exportName: "user4", optional: true},
	{id: "subsurface", mapName: Properties, part: "R", painter: "User1", format: "L8", label: "Subsurface", exportType: "documentMap", exportName: "user1"},
	{id: "specular", mapName: Properties, part: "G", painter: "User2", format: "L8", label: "Specular strength", exportType: "documentMap", exportName: "user2"},
	{id: "metallic", mapName: Properties, part: "B", painter: "Metallic", format: "L8", exportType: "documentMap", exportName: "metallic"},
	{id: "roughness", mapName: Properties, part: "A", painter: "Roughness", format: "L8", exportType: "documentMap", exportName: "roughness"},
}

// starts are the values the channels of a new map start at in Painter: a
// mid grey, opaque, a little specular and half rough, which is what most of
// the games' materials are near. The normal map starts flat, as Painter
// starts it.
var starts = map[string]float64{
	"basecolor":    0.5,
	"diffusealpha": 1,
	"subsurface":   0,
	"specular":     0.5,
	"metallic":     0,
	"roughness":    0.5,
}

// blanks are the pixels a new map is filled with before what Painter
// exported is laid in, which the channels Painter has nothing of keep: a
// flat normal has its x and y, in green and alpha, at the middle.
var blanks = map[string][4]uint8{
	Diffuse:    {128, 128, 128, 255},
	Normal:     {0, 128, 0, 128},
	Properties: {0, 128, 0, 128},
}

// newFormat is what a new map is stored as: DXT5, which every one of the
// games' maps reads, alpha and all.
const newFormat = "BC3_UNORM"

func specOf(id string) (spec, bool) {
	for _, s := range specs {
		if s.id == id {
			return s, true
		}
	}

	return spec{}, false
}

func component(part string) int {
	switch part {
	case "R":
		return 0
	case "G":
		return 1
	case "B":
		return 2
	}

	return 3
}

// varies reports whether one channel of an image holds more than one value.
func varies(img *image.NRGBA, c int) bool {
	first := img.Pix[c]

	for i := c; i < len(img.Pix); i += 4 {
		if img.Pix[i] != first {
			return true
		}
	}

	return false
}

// extract is the image of what a channel holds of a map, as Painter imports
// it.
func (s spec) extract(source *image.NRGBA, flipGreen bool) image.Image {
	bounds := source.Rect

	switch s.part {
	case "rgb":
		out := image.NewNRGBA(bounds)
		for i := 0; i < len(source.Pix); i += 4 {
			copy(out.Pix[i:i+3], source.Pix[i:i+3])
			out.Pix[i+3] = 255
		}

		return out

	case "normal":
		out := image.NewNRGBA(bounds)
		for i := 0; i < len(source.Pix); i += 4 {
			x, y := source.Pix[i+1], source.Pix[i+3]
			if flipGreen {
				y = 255 - y
			}

			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = x, y, normalZ(x, y), 255
		}

		return out
	}

	c := component(s.part)
	out := image.NewGray(bounds)

	for i := range out.Pix {
		out.Pix[i] = source.Pix[i*4+c]
	}

	return out
}

// normalZ is the z of a normal from its x and y, which the games leave out.
func normalZ(x, y uint8) uint8 {
	fx := float64(x)/127.5 - 1
	fy := float64(y)/127.5 - 1

	return uint8(math.Round((math.Sqrt(max(1-fx*fx-fy*fy, 0)) + 1) * 127.5))
}

// insert lays what Painter exported of a channel into a map, the exported
// image already brought to the map's size.
func (s spec) insert(target, exported *image.NRGBA, flipGreen bool) {
	switch s.part {
	case "rgb":
		for i := 0; i < len(target.Pix); i += 4 {
			copy(target.Pix[i:i+3], exported.Pix[i:i+3])
		}

	case "normal":
		for i := 0; i < len(target.Pix); i += 4 {
			y := exported.Pix[i+1]
			if flipGreen {
				y = 255 - y
			}

			target.Pix[i+1], target.Pix[i+3] = exported.Pix[i], y
		}

	default:
		c := component(s.part)
		for i := 0; i < len(target.Pix); i += 4 {
			target.Pix[i+c] = exported.Pix[i]
		}
	}
}
