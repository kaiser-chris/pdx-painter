package painter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaiser-chris/dds-go"

	"github.com/kaiser-chris/pdx-painter/internal/locate"
)

// houseOBJ is a model of the user's own as Painter exports it, a material for
// each of its two Texture Sets: a wall of a quad and a roof of a triangle.
const houseOBJ = `v -1 0 0
v 1 0 0
v 1 2 0
v -1 2 0
vt 0 0
vt 1 0
vt 1 1
vt 0 1
vn 0 0 1
usemtl wall
f 1/1/1 2/2/1 3/3/1 4/4/1
usemtl roof
f 4/4/1 3/3/1 2/2/1
`

func TestPrepareForeign(t *testing.T) {
	manifest, err := PrepareForeign("house", []string{"wall", "roof"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if manifest.Kind != KindForeign || len(manifest.TextureSets) != 2 || manifest.MeshFile != nil {
		t.Fatalf("manifest = %+v", manifest)
	}

	wall := manifest.TextureSets[0]
	if stored := wall.Maps[Normal]; !stored.New || stored.Format != newFormat || stored.Relative != "wall_normal.dds" {
		t.Errorf("normal = %+v", stored)
	}

	if len(wall.Channels) != 7 || wall.Meshes[0].Shape != "wall" || wall.Meshes[0].ShaderFile != "gfx/FX/pdxmesh.shader" {
		t.Errorf("wall = %d channels, meshes %+v", len(wall.Channels), wall.Meshes)
	}

	if _, err := PrepareForeign("house", nil, t.TempDir()); err == nil {
		t.Error("a project of no Texture Set was prepared")
	}

	if _, err := PrepareForeign("house", []string{"a", "a"}, t.TempDir()); err == nil {
		t.Error("two sets of one name were prepared")
	}
}

// A project of the user's own is exported as an entity of the games: its maps
// made from Painter's channels, its mesh from Painter's OBJ, and an asset
// drawing them, which the games load.
func TestPackAForeignProject(t *testing.T) {
	manifest, err := PrepareForeign("house", []string{"wall", "roof"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	exported := t.TempDir()
	solid(t, filepath.Join(exported, "wall_basecolor.png"), 32, [4]byte{180, 60, 20, 255})
	solid(t, filepath.Join(exported, "wall_normal.png"), 32, [4]byte{128, 128, 255, 255})
	solid(t, filepath.Join(exported, "wall_roughness.png"), 32, [4]byte{200, 200, 200, 255})
	solid(t, filepath.Join(exported, "roof_basecolor.png"), 16, [4]byte{20, 20, 160, 255})

	obj := filepath.Join(t.TempDir(), "painter.obj")
	if err := os.WriteFile(obj, []byte(houseOBJ), 0o644); err != nil {
		t.Fatal(err)
	}

	folder := t.TempDir()

	var maps []PlannedMap
	for _, set := range []string{"wall", "roof"} {
		for _, kind := range []string{Diffuse, Normal, Properties} {
			maps = append(maps, PlannedMap{Set: set, Kind: kind, File: "house_" + set + "_" + kind + ".dds"})
		}
	}

	plan := &Plan{
		Folder: folder,
		Maps:   maps,
		Mesh:   &PlannedMesh{File: "house.mesh", OBJ: obj, Scale: 0.5},
		Asset:  &PlannedAsset{File: "house.asset", Mesh: "house_mesh", Entity: "house_entity"},
	}

	packed, err := Pack(manifest, PackOptions{Exported: exported, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}

	// Every map is new, so every one is written, the roof's normal and
	// properties of nothing but their starting values.
	if len(packed.Written) != 8 {
		t.Errorf("written %v, want six maps, the mesh and the asset", packed.Written)
	}

	diffuse := readBack(t, filepath.Join(folder, "house_wall_diffuse.dds"), dds.BC3Unorm, 32, 6)
	if c, _ := diffuse.Colour(4, 4); c[0] < 170 || c[3] != 255 {
		t.Errorf("wall diffuse = %v", c)
	}

	readBack(t, filepath.Join(folder, "house_roof_normal.dds"), dds.BC3Unorm, 16, 5)

	// The games load it, both shapes drawn with their maps.
	location, err := locate.Locate(filepath.Join(folder, "house.asset"))
	if err != nil {
		t.Fatal(err)
	}

	opened, err := locate.Open(location, "")
	if err != nil {
		t.Fatal(err)
	}

	built, problems, err := opened.Loader.Load("house_entity")
	if err != nil {
		t.Fatal(err)
	}

	if len(problems) != 0 || len(built.Parts) != 2 {
		t.Fatalf("loaded %d parts, problems %v", len(built.Parts), problems)
	}

	for _, part := range built.Parts {
		files := part.Textures.Files
		if !files.Diffuse.Found() || !files.Normal.Found() || !files.Properties.Found() || part.Shader != "standard" {
			t.Errorf("part %s draws %+v with %q", part.Name, files, part.Shader)
		}
	}

	// Scaled by a half: the wall, two high in the OBJ, is one high.
	if built.Max[1] != 1 {
		t.Errorf("the model is %v high, want 1", built.Max[1])
	}
}

// Without the mesh, the asset names the one the user makes.
func TestPackAForeignAssetOfAMeshOfTheUsers(t *testing.T) {
	manifest, err := PrepareForeign("house", []string{"wall"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	folder := t.TempDir()
	plan := &Plan{
		Folder: folder,
		Asset:  &PlannedAsset{File: "house.asset", Mesh: "house_mesh", Entity: "house_entity", MeshFile: "my_house.mesh"},
	}

	if _, err := Pack(manifest, PackOptions{Exported: t.TempDir(), Plan: plan}); err != nil {
		t.Fatal(err)
	}

	text, _ := os.ReadFile(filepath.Join(folder, "house.asset"))
	if !strings.Contains(string(text), `file = "my_house.mesh"`) || strings.Contains(string(text), "texture_diffuse") {
		t.Errorf("the asset is:\n%s\nwant it to name my_house.mesh, and no maps, none being written", text)
	}

	// Naming no mesh at all is refused.
	plan.Asset.MeshFile = ""
	if _, err := Pack(manifest, PackOptions{Exported: t.TempDir(), Plan: plan}); err == nil {
		t.Error("an asset of no mesh was written")
	}
}
