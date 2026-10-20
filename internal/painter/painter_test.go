package painter

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaiser-chris/pdx-parser-go/asset"
	"github.com/kaiser-chris/pdx-parser-go/folders"

	"github.com/kaiser-chris/pdx-asset-go/entity"
	"github.com/kaiser-chris/pdx-asset-go/mesh/meshtest"
	"github.com/kaiser-chris/pdx-asset-go/texture"

	"github.com/kaiser-chris/pdx-painter/internal/dds"
	"github.com/kaiser-chris/pdx-painter/internal/locate"
)

const statueAsset = `
pdxmesh = {
	name = "statue_mesh"
	file = "statue.mesh"
	meshsettings = {
		name = "quadShape"
		index = 0
		texture_diffuse = "statue_diffuse.dds"
		texture_normal = "statue_normal.dds"
		texture_specular = "statue_properties.dds"
		shader = "standard"
	}
}

entity = { name = "statue_entity" pdxmesh = "statue_mesh" }
`

// pattern is an image whose every channel holds something of its own: ramps
// in red and green, a flat blue, and alpha in two halves.
func pattern(size int, blue uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := range size {
		for x := range size {
			p := img.Pix[y*img.Stride+x*4:]
			p[0], p[1], p[2], p[3] = uint8(x*255/(size-1)), uint8(y*255/(size-1)), blue, 255

			if x >= size/2 {
				p[3] = 64
			}
		}
	}

	return img
}

func encoded(t *testing.T, img *image.NRGBA, format dds.Format) []byte {
	t.Helper()

	data, err := dds.Encode(img, format, 99, false)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// statueGame writes a game folder with one statue whose maps are stored the
// three ways the games store theirs.
func statueGame(t *testing.T) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), "game")
	files := map[string][]byte{
		"gfx/models/statue/statue.asset":          []byte(statueAsset),
		"gfx/models/statue/statue.mesh":           meshtest.QuadFile(2, 2),
		"gfx/models/statue/statue_diffuse.dds":    encoded(t, pattern(16, 30), dds.BC3),
		"gfx/models/statue/statue_normal.dds":     encoded(t, pattern(16, 0), dds.BGRA8),
		"gfx/models/statue/statue_properties.dds": encoded(t, pattern(8, 200), dds.BC7),
	}

	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func prepared(t *testing.T, options Options) (*Manifest, string) {
	t.Helper()

	root := statueGame(t)

	location, err := locate.Locate(filepath.Join(root, "gfx", "models", "statue", "statue.asset"))
	if err != nil {
		t.Fatal(err)
	}

	if location.Kind != locate.Game || location.Root != root {
		t.Fatalf("location = %+v", location)
	}

	opened, err := locate.Open(location, "")
	if err != nil {
		t.Fatal(err)
	}

	folder := filepath.Join(t.TempDir(), "work")

	manifest, err := Prepare(opened, "statue_entity", folder, options)
	if err != nil {
		t.Fatal(err)
	}

	return manifest, folder
}

// export stands in for Painter's export: the PNGs prepare wrote, named the way
// the plugin's export preset names them.
func export(t *testing.T, manifest *Manifest) string {
	t.Helper()

	folder := filepath.Join(t.TempDir(), "exported")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, set := range manifest.TextureSets {
		for _, channel := range set.Channels {
			data, err := os.ReadFile(channel.Image)
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(folder, set.Name+"_"+channel.ID+".png"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	return folder
}

func TestPrepareAnEntity(t *testing.T) {
	manifest, folder := prepared(t, Options{})

	if len(manifest.TextureSets) != 1 {
		t.Fatalf("texture sets = %+v", manifest.TextureSets)
	}

	set := manifest.TextureSets[0]
	if set.Name != "statue" || set.Width != 16 || set.Height != 16 {
		t.Errorf("set %s of %dx%d, want statue of 16x16, the largest map", set.Name, set.Width, set.Height)
	}

	if stored := set.Maps[Properties]; stored.Format != "BC7" || stored.Levels != 4 || stored.Width != 8 {
		t.Errorf("properties = %+v", stored)
	}

	var ids []string
	for _, channel := range set.Channels {
		ids = append(ids, channel.ID)
	}

	// The normal map's blue is flat, so it gets no channel; its red holds a
	// ramp, so it does.
	if got := strings.Join(ids, " "); got != "basecolor diffusealpha normal normalred subsurface specular metallic roughness" {
		t.Errorf("channels = %s", got)
	}

	obj, err := os.ReadFile(manifest.Mesh)
	if err != nil {
		t.Fatal(err)
	}

	if text := string(obj); !strings.Contains(text, "usemtl statue\n") || strings.Count(text, "\nv ") != 4 || strings.Count(text, "\nf ") != 2 {
		t.Errorf("OBJ =\n%s", text)
	}

	if _, err := os.Stat(filepath.Join(folder, ManifestFile)); err != nil {
		t.Error(err)
	}
}

// The normal map's x and y, in green and alpha, are red and green for
// Painter, with the z worked out in blue.
func TestPrepareUnpacksTheNormalMap(t *testing.T) {
	for _, flip := range []bool{false, true} {
		manifest, _ := prepared(t, Options{FlipGreen: flip})

		img, err := readPNG(manifest.TextureSets[0].Channels[2].Image)
		if err != nil {
			t.Fatal(err)
		}

		// At x 15, y 0 of pattern: green 0, alpha 64.
		p := img.Pix[15*4 : 15*4+4]

		wantY := uint8(64)
		if flip {
			wantY = 191
		}

		if p[0] != 0 || p[1] != wantY || p[2] < 128 {
			t.Errorf("flip %v: normal = %v, want x 0 and y %d", flip, p, wantY)
		}
	}
}

// What Painter gives back unchanged is not written again, and what it
// changes is laid into the channels it came from, the rest kept.
func TestPackWritesWhatChanged(t *testing.T) {
	manifest, _ := prepared(t, Options{})
	exported := export(t, manifest)
	out := filepath.Join(t.TempDir(), "mod")

	packed, err := Pack(manifest, PackOptions{Exported: exported, Out: out, Backup: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(packed.Written) != 0 || len(packed.Unchanged) != 3 {
		t.Fatalf("packed = %+v, want every map unchanged", packed)
	}

	// Painted red all over, and the roughness turned white.
	red := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(red.Pix); i += 4 {
		copy(red.Pix[i:], []byte{255, 0, 0, 255})
	}

	white := image.NewGray(image.Rect(0, 0, 16, 16))
	for i := range white.Pix {
		white.Pix[i] = 255
	}

	if err := writePNG(filepath.Join(exported, "statue_basecolor.png"), red); err != nil {
		t.Fatal(err)
	}

	if err := writePNG(filepath.Join(exported, "statue_roughness.png"), white); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(exported, "statue_specular.png")); err != nil {
		t.Fatal(err)
	}

	packed, err = Pack(manifest, PackOptions{Exported: exported, Out: out, Backup: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(packed.Written) != 2 || len(packed.Notes) != 1 {
		t.Fatalf("packed = %+v, want the diffuse and properties written and the specular noted", packed)
	}

	diffuse := readBack(t, filepath.Join(out, "statue_diffuse.dds"), dds.BC3, 16, 5)
	if c, _ := diffuse.Colour(0, 0); c[0] < 250 || c[1] > 5 || c[3] != 255 {
		t.Errorf("diffuse at the left = %v, want red with its alpha kept", c)
	}

	if c, _ := diffuse.Colour(15, 0); c[3] != 64 {
		t.Errorf("diffuse at the right = %v, want the alpha of 64 kept", c)
	}

	// Brought from 16 pixels back to the 8 of the map, and its other
	// channels as they were: the blue of 200 is the metalness.
	properties := readBack(t, filepath.Join(out, "statue_properties.dds"), dds.BC7, 8, 4)
	if c, _ := properties.Colour(3, 3); c[3] < 254 || c[2] < 198 || c[2] > 202 {
		t.Errorf("properties = %v, want roughness white and metalness kept", c)
	}

	// There was nothing in the mod to back up; a second pack backs up the
	// first's.
	if _, err := os.Stat(filepath.Join(out, "statue_diffuse.dds.bak")); !os.IsNotExist(err) {
		t.Errorf("a backup of nothing: %v", err)
	}

	if _, err := Pack(manifest, PackOptions{Exported: exported, Out: out, Backup: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(out, "statue_diffuse.dds.bak")); err != nil {
		t.Errorf("no backup: %v", err)
	}
}

// Only what the mesh covers in texture space is taken from Painter: the rest
// of a map may be another model's.
func TestPackKeepsWhatTheMeshDoesNotCover(t *testing.T) {
	manifest, _ := prepared(t, Options{})
	set := manifest.TextureSets[0]

	// The quad covers the whole texture, and a margin is no matter there.
	mask := readMask(set.Coverage)
	if mask == nil || mask.Rect.Dx() != 16 || mask.Pix[0] != 255 || mask.Pix[len(mask.Pix)-1] != 255 {
		t.Fatalf("coverage of the quad = %v", mask)
	}

	// Made to cover the left half only, as the parts of another model would
	// leave the right half to it.
	half := image.NewGray(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 8 {
			half.Pix[y*16+x] = 255
		}
	}

	if err := writePNG(set.Coverage, half); err != nil {
		t.Fatal(err)
	}

	exported := export(t, manifest)
	red := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(red.Pix); i += 4 {
		copy(red.Pix[i:], []byte{255, 0, 0, 255})
	}

	if err := writePNG(filepath.Join(exported, "statue_basecolor.png"), red); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if _, err := Pack(manifest, PackOptions{Exported: exported, Out: out}); err != nil {
		t.Fatal(err)
	}

	diffuse := readBack(t, filepath.Join(out, "statue_diffuse.dds"), dds.BC3, 16, 5)

	if c, _ := diffuse.Colour(2, 8); c[0] < 250 || c[1] > 5 {
		t.Errorf("left = %v, want red", c)
	}

	// The right half is the original's, compressed once more.
	original, err := readPixels(set.Maps[Diffuse].Path)
	if err != nil {
		t.Fatal(err)
	}

	for _, x := range []int{9, 13, 15} {
		c, _ := diffuse.Colour(x, 15)
		want := original.Pix[15*original.Stride+x*4:]

		for channel := range 4 {
			if d := int(c[channel]) - int(want[channel]); d < -8 || d > 8 {
				t.Errorf("right at %d = %v, want the original's %v", x, c, want[:4])

				break
			}
		}
	}
}

func TestCoverageOfATriangle(t *testing.T) {
	mask := image.NewGray(image.Rect(0, 0, 32, 32))

	// The top left half of the texture, and a sliver thinner than a texel
	// beyond its right edge, which wraps round to the left.
	fill(mask, [3][2]float64{{0, 0}, {16, 0}, {0, 16}})
	fill(mask, [3][2]float64{{40.2, 20}, {40.4, 30}, {40.3, 25}})

	if mask.Pix[2*32+2] == 0 || mask.Pix[20*32+20] != 0 {
		t.Error("the half triangle is not where it is")
	}

	if mask.Pix[20*32+8] == 0 {
		t.Error("the sliver beyond the edge marked nothing where it wraps to")
	}

	wide := dilate(mask, 2)
	if wide.Pix[20*32+10] == 0 || wide.Pix[20*32+11] != 0 {
		t.Error("the margin is not two texels")
	}
}

// The y of the normals goes back the way it came, flipped or not.
func TestPackRoundTripsTheNormalMap(t *testing.T) {
	for _, flip := range []bool{false, true} {
		manifest, _ := prepared(t, Options{FlipGreen: flip})
		exported := export(t, manifest)

		packed, err := Pack(manifest, PackOptions{Exported: exported, Out: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}

		if len(packed.Written) != 0 {
			t.Errorf("flip %v: written %v, want the normal map back as it was", flip, packed.Written)
		}
	}
}

func readBack(t *testing.T, path string, format dds.Format, size, levels int) *texture.Image {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	header, err := dds.ReadHeader(data)
	if err != nil {
		t.Fatal(err)
	}

	if header.Format != format || header.Width != size || header.Levels != levels {
		t.Errorf("%s: header = %+v, want %s of %d with %d levels", filepath.Base(path), header, format, size, levels)
	}

	img, err := texture.DecodeDDS(data)
	if err != nil {
		t.Fatal(err)
	}

	return img
}

// solid is a PNG of one colour, as Painter exports a channel.
func solid(t *testing.T, path string, size int, colour [4]byte) {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], colour[:])
	}

	if err := writePNG(path, img); err != nil {
		t.Fatal(err)
	}
}

// A mesh file of its own gets maps named after it, whatever its material
// names: those next to it are read, and the rest are new, which packing makes
// next to it at the size Painter painted.
func TestPrepareAndPackAMeshOfItsOwn(t *testing.T) {
	folder := t.TempDir()
	meshFile := filepath.Join(folder, "statue.mesh")

	// The quad's material names quad_diffuse.dds, which is there and is
	// not the statue's.
	if err := os.WriteFile(meshFile, meshtest.QuadFile(2, 2), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(folder, "quad_diffuse.dds"), encoded(t, pattern(16, 0), dds.BC3), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := PrepareMesh(meshFile, filepath.Join(t.TempDir(), "work"), Options{})
	if err != nil {
		t.Fatal(err)
	}

	if manifest.Kind != KindMesh || len(manifest.TextureSets) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}

	set := manifest.TextureSets[0]
	if set.Name != "statue" || set.Width != newSize {
		t.Errorf("set %s of %d, want statue, after the mesh, of %d", set.Name, set.Width, newSize)
	}

	for kind, name := range map[string]string{Diffuse: "statue_diffuse.dds", Normal: "statue_normal.dds", Properties: "statue_properties.dds"} {
		if stored := set.Maps[kind]; !stored.New || stored.Path != filepath.Join(folder, name) || stored.Format != "BC3" {
			t.Errorf("%s = %+v, want a new %s", kind, stored, name)
		}
	}

	var ids []string
	for _, channel := range set.Channels {
		if channel.Image != "" {
			t.Errorf("%s of a new map has an image to import", channel.ID)
		}

		ids = append(ids, channel.ID)
	}

	if got := strings.Join(ids, " "); got != "basecolor diffusealpha normal subsurface specular metallic roughness" {
		t.Errorf("channels = %s, without the masks only some shaders read", got)
	}

	if set.Channels[1].Default == nil || *set.Channels[1].Default != 1 || set.Channels[2].Default != nil {
		t.Error("the diffuse alpha starts opaque, the normal as Painter starts it")
	}

	exported := t.TempDir()
	solid(t, filepath.Join(exported, "statue_basecolor.png"), 32, [4]byte{200, 0, 0, 255})
	solid(t, filepath.Join(exported, "statue_diffusealpha.png"), 32, [4]byte{255, 255, 255, 255})
	solid(t, filepath.Join(exported, "statue_normal.png"), 32, [4]byte{128, 128, 255, 255})
	solid(t, filepath.Join(exported, "statue_roughness.png"), 32, [4]byte{64, 64, 64, 255})

	packed, err := Pack(manifest, PackOptions{Exported: exported, InPlace: true, Backup: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(packed.Written) != 3 {
		t.Fatalf("packed = %+v, want the three new maps written", packed)
	}

	diffuse := readBack(t, filepath.Join(folder, "statue_diffuse.dds"), dds.BC3, 32, 6)
	if c, _ := diffuse.Colour(5, 5); c[0] < 195 || c[0] > 205 || c[3] != 255 {
		t.Errorf("diffuse = %v", c)
	}

	normal := readBack(t, filepath.Join(folder, "statue_normal.dds"), dds.BC3, 32, 6)
	if c, _ := normal.Colour(5, 5); c[1] < 124 || c[1] > 132 || c[3] < 124 || c[3] > 132 {
		t.Errorf("normal = %v, want flat", c)
	}

	// The specular strength was not exported, and keeps what a new map
	// starts at.
	properties := readBack(t, filepath.Join(folder, "statue_properties.dds"), dds.BC3, 32, 6)
	if c, _ := properties.Colour(5, 5); c[3] < 60 || c[3] > 68 || c[1] < 124 || c[1] > 132 {
		t.Errorf("properties = %v", c)
	}

	// Opened again, the maps are there now and are read as an entity's are.
	again, err := PrepareMesh(meshFile, filepath.Join(t.TempDir(), "work"), Options{})
	if err != nil {
		t.Fatal(err)
	}

	if stored := again.TextureSets[0].Maps[Diffuse]; stored.New || again.TextureSets[0].Channels[0].Image == "" {
		t.Errorf("diffuse = %+v, want the map now next to the mesh", stored)
	}
}

// In place, every map goes over the file it was read from, kept as a
// backup.
func TestPackInPlace(t *testing.T) {
	manifest, _ := prepared(t, Options{})
	exported := export(t, manifest)
	solid(t, filepath.Join(exported, "statue_basecolor.png"), 16, [4]byte{0, 0, 255, 255})

	packed, err := Pack(manifest, PackOptions{Exported: exported, InPlace: true, Backup: true})
	if err != nil {
		t.Fatal(err)
	}

	path := manifest.TextureSets[0].Maps[Diffuse].Path
	if len(packed.Written) != 1 || packed.Written[0] != path {
		t.Fatalf("written %v, want %s", packed.Written, path)
	}

	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("no backup of the original: %v", err)
	}
}

// Two maps of one name from different folders cannot both go straight into
// one folder, which is said before anything is written.
func TestPackRefusesMapsOfOneName(t *testing.T) {
	manifest, _ := prepared(t, Options{})
	exported := export(t, manifest)
	solid(t, filepath.Join(exported, "statue_basecolor.png"), 16, [4]byte{0, 0, 255, 255})
	solid(t, filepath.Join(exported, "statue_roughness.png"), 16, [4]byte{9, 9, 9, 255})

	// The properties map renamed as the diffuse is, in another folder.
	set := manifest.TextureSets[0]
	properties := set.Maps[Properties]
	properties.Relative = "gfx/other/statue_diffuse.dds"
	set.Maps[Properties] = properties

	out := t.TempDir()
	if _, err := Pack(manifest, PackOptions{Exported: exported, Out: out}); err == nil {
		t.Fatal("two maps were written to one file")
	}

	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("written before refusing: %v", entries)
	}
}

// A mesh of several shapes gets a set for each, its maps named after the mesh,
// those after the first numbered.
func TestPrepareAMeshOfSeveralShapes(t *testing.T) {
	folder := t.TempDir()
	meshFile := filepath.Join(folder, "house.mesh")

	data := meshtest.New().Object(1, "object").
		Object(2, "LOD_0|house|house_wallsShape").Mesh(meshtest.Quad(2, 2), "walls_diffuse.dds").
		Object(2, "house_roofShape").Mesh(meshtest.Quad(1, 1), "").
		Bytes()

	if err := os.WriteFile(meshFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := PrepareMesh(meshFile, filepath.Join(t.TempDir(), "work"), Options{})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, set := range manifest.TextureSets {
		got = append(got, set.Name+" "+set.Maps[Diffuse].Relative)
	}

	if want := "house house_diffuse.dds|house_2 house_2_diffuse.dds"; strings.Join(got, "|") != want {
		t.Errorf("sets = %s, want %s", strings.Join(got, "|"), want)
	}
}

// An advanced export saves what was opened as something new: the maps chosen,
// by new names and formats, a copy of the mesh and an asset drawing it, which
// the game loads as an entity of its own with the maps not chosen taken from
// where they were.
func TestPackAPlanAsANewEntity(t *testing.T) {
	manifest, _ := prepared(t, Options{})
	exported := export(t, manifest)
	solid(t, filepath.Join(exported, "statue_basecolor.png"), 16, [4]byte{0, 200, 0, 255})

	game := manifest.Root
	mod := filepath.Join(t.TempDir(), "mod")
	folder := filepath.Join(mod, "gfx", "models", "monument")

	if manifest.MeshFile == nil || manifest.MeshFile.Relative != "gfx/models/statue/statue.mesh" {
		t.Fatalf("mesh file = %+v", manifest.MeshFile)
	}

	if meshes := manifest.TextureSets[0].Meshes; len(meshes) != 1 || meshes[0].Shape != "quadShape" || meshes[0].Shader != "standard" {
		t.Fatalf("meshes = %+v", meshes)
	}

	plan := &Plan{
		Folder: folder,
		Maps: []PlannedMap{
			{Set: "statue", Kind: Diffuse, File: "monument_diffuse.dds", Format: "BC7"},
			{Set: "statue", Kind: Properties, File: "monument_properties.dds"},
		},
		Mesh:  &PlannedMesh{File: "monument.mesh"},
		Asset: &PlannedAsset{File: "monument.asset", Mesh: "monument_mesh", Entity: "monument_entity"},
	}

	packed, err := Pack(manifest, PackOptions{Exported: exported, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}

	// The properties map is as it was, and is written as asked all the same:
	// it is a new file.
	if len(packed.Written) != 4 {
		t.Errorf("written %v, want two maps, the mesh and the asset", packed.Written)
	}

	readBack(t, filepath.Join(folder, "monument_diffuse.dds"), dds.BC7, 16, 5)
	readBack(t, filepath.Join(folder, "monument_properties.dds"), dds.BC7, 8, 4)

	original, _ := os.ReadFile(manifest.MeshFile.Path)
	if copied, _ := os.ReadFile(filepath.Join(folder, "monument.mesh")); string(copied) != string(original) {
		t.Error("the mesh was not copied as it is")
	}

	text, err := os.ReadFile(filepath.Join(folder, "monument.asset"))
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{
		`name = "monument_mesh"`, `file = "monument.mesh"`, `name = "quadShape"`, `index = 0`,
		`texture_diffuse = "monument_diffuse.dds"`, `texture_normal = "statue_normal.dds"`,
		`texture_specular = "monument_properties.dds"`, `shader = "standard"`, `shader_file = "gfx/FX/pdxmesh.shader"`,
		`name = "monument_entity"`, `pdxmesh = "monument_mesh"`,
	} {
		if !strings.Contains(string(text), line) {
			t.Errorf("the asset has no %s:\n%s", line, text)
		}
	}

	// The game loads it, the normal map not chosen found where it was.
	set := folders.Open([]folders.Source{{Name: "game", Path: game}, {Name: "mod", Path: mod}})

	built, _, err := entity.NewLoader(set, asset.Load(set)).Load("monument_entity")
	if err != nil {
		t.Fatal(err)
	}

	files := built.Parts[0].Textures.Files
	if files.Diffuse.Relative != "gfx/models/monument/monument_diffuse.dds" || files.Normal.Relative != "gfx/models/statue/statue_normal.dds" {
		t.Errorf("the new entity draws %+v", files)
	}

	if c, _ := built.Parts[0].Textures.Diffuse.Colour(3, 3); c[1] < 190 || c[0] > 10 {
		t.Errorf("the new entity's diffuse = %v, want the green painted", c)
	}

	// The files it came from are as they were.
	if _, err := os.Stat(filepath.Join(game, "gfx", "models", "statue", "statue_diffuse.dds.bak")); !os.IsNotExist(err) {
		t.Error("the original was written over")
	}
}

// A plan that cannot be carried out is refused before anything is written.
func TestPlanIsChecked(t *testing.T) {
	manifest, _ := prepared(t, Options{})
	exported := export(t, manifest)

	cases := map[string]Plan{
		"no folder":       {Maps: []PlannedMap{{Set: "statue", Kind: Diffuse, File: "a.dds"}}},
		"no set":          {Maps: []PlannedMap{{Set: "nothing", Kind: Diffuse, File: "a.dds"}}},
		"a path":          {Maps: []PlannedMap{{Set: "statue", Kind: Diffuse, File: "sub/a.dds"}}},
		"wrong extension": {Maps: []PlannedMap{{Set: "statue", Kind: Diffuse, File: "a.png", Format: "BC7"}}},
		"no format":       {Maps: []PlannedMap{{Set: "statue", Kind: Diffuse, File: "a.dds", Format: "ATI2"}}},
		"one file twice":  {Maps: []PlannedMap{{Set: "statue", Kind: Diffuse, File: "a.dds"}, {Set: "statue", Kind: Normal, File: "A.dds"}}},
		"no entity name":  {Asset: &PlannedAsset{File: "a.asset", Mesh: "a_mesh"}},
	}

	for name, plan := range cases {
		out := t.TempDir()
		if plan.Folder == "" && name != "no folder" {
			plan.Folder = out
		}

		if _, err := Pack(manifest, PackOptions{Exported: exported, Plan: &plan}); err == nil {
			t.Errorf("%s: carried out", name)
		}

		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Errorf("%s: written before refusing: %v", name, entries)
		}
	}
}
