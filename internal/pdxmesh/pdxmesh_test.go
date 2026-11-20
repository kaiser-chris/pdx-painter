package pdxmesh

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kaiser-chris/pdx-asset-go/mesh"
	"github.com/kaiser-chris/pdx-asset-go/model"
)

// quadOBJ is a square in the plane z = 0, facing +z, as Painter exports one:
// right handed, wound anticlockwise, v counted up from the bottom. A second
// material holds one triangle without normals or texture coordinates.
const quadOBJ = `# Painter
mtllib quad.mtl
v -1 -1 0
v 1 -1 0
v 1 1 0
v -1 1 0
vt 0 0
vt 1 0
vt 1 1
vt 0 1
vn 0 0 1
usemtl wall
f 1/1/1 2/2/1 3/3/1 4/4/1
usemtl roof
f 1 2 3
`

func readQuad(t *testing.T) []Group {
	t.Helper()

	groups, err := ReadOBJ(strings.NewReader(quadOBJ))
	if err != nil {
		t.Fatal(err)
	}

	return groups
}

func TestReadOBJ(t *testing.T) {
	groups := readQuad(t)

	if len(groups) != 2 || groups[0].Material != "wall" || groups[1].Material != "roof" {
		t.Fatalf("groups = %+v", groups)
	}

	// The quad, cut into a fan of two triangles over four shared vertices.
	if wall := groups[0]; len(wall.Positions) != 12 || len(wall.Indices) != 6 {
		t.Errorf("wall = %d positions, %d indices", len(wall.Positions)/3, len(wall.Indices)/3)
	}

	// The roof's corners have no normal, and take their face's.
	if roof := groups[1]; roof.Normals[2] != 1 {
		t.Errorf("roof normal = %v, want the face's, +z", roof.Normals[:3])
	}
}

func TestReadOBJRefusesFacesOfNothing(t *testing.T) {
	for _, text := range []string{"f 1 2 3\n", "v 0 0 0\nf 1 1\n", "v 0 0 0\nf 1/x 1 1\n"} {
		if _, err := ReadOBJ(strings.NewReader(text)); err == nil {
			t.Errorf("%q was read", text)
		}
	}
}

// Converted and written, the quad reads back in the games' coordinates:
// mirrored across x, v counted down from the top, wound the other way round,
// and its tangent along u, the bitangent's sign saying v runs up the
// picture's rows the other way.
func TestWriteReadsBackAsTheGames(t *testing.T) {
	wall := readQuad(t)[0]

	data, err := Write([]Shape{{Name: "wallShape", Meshes: []Mesh{
		Convert(wall, 2, Material{Shader: "standard", Diffuse: "house_diffuse.dds", Normal: "house_normal.dds"}),
	}}})
	if err != nil {
		t.Fatal(err)
	}

	read, err := mesh.Read(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(read.Warnings) != 0 || len(read.Shapes) != 1 || read.Shapes[0].Name != "wallShape" || read.Shapes[0].LOD != 0 {
		t.Fatalf("read = %+v", read)
	}

	m := read.Shapes[0].Meshes[0]

	// The first corner, at (-1, -1, 0) and (0, 0), scaled by two.
	if p := m.Positions[:3]; p[0] != 2 || p[1] != -2 || p[2] != 0 {
		t.Errorf("position = %v, want (2, -2, 0)", p)
	}

	if uv := m.UV(0)[:2]; uv[0] != 0 || uv[1] != 1 {
		t.Errorf("uv = %v, want (0, 1): the bottom row of the picture", uv)
	}

	if n := m.Normals[:3]; n[2] != 1 {
		t.Errorf("normal = %v", n)
	}

	// u grows along -x once mirrored; v grows down the picture, along -y.
	if ta := m.Tangents[:4]; ta[0] != -1 || ta[1] != 0 || ta[3] != 1 {
		t.Errorf("tangent = %v, want (-1, 0, 0) and w 1", ta)
	}

	if m.Material.Shader != "standard" || m.Material.Diffuse != "house_diffuse.dds" || m.Material.Specular != "" {
		t.Errorf("material = %+v", m.Material)
	}

	if m.Min != [3]float32{-2, -2, 0} || m.Max != [3]float32{2, 2, 0} || math.Abs(float64(m.Sphere[3])-math.Sqrt(8)) > 1e-5 {
		t.Errorf("box %v to %v, sphere %v", m.Min, m.Max, m.Sphere)
	}

	// Drawn the way pdx-asset-go draws the games' meshes, it is the quad of
	// the OBJ again: mirrored back, and wound as it was.
	pieces := model.Convert(&m)
	if p := pieces[0].Positions[:3]; p[0] != -2 || p[1] != -2 {
		t.Errorf("drawn at %v, want (-2, -2, 0)", p)
	}

	for index, vertex := range pieces[0].Indices {
		if int32(vertex) != wall.Indices[index] {
			t.Fatalf("drawn wound %v, want %v", pieces[0].Indices, wall.Indices)
		}
	}
}

// Over a real mesh: the games' own, drawn into an OBJ the way the plugin opens
// it in Painter, and converted back, is the mesh again, its tangents the ones
// its exporter worked out. It needs a game: PDX_GAME_DIR.
func TestConvertBackAnInstalledMesh(t *testing.T) {
	root := os.Getenv("PDX_GAME_DIR")
	if root == "" {
		t.Skip("set PDX_GAME_DIR to the game folder to run this test")
	}

	path := filepath.Join(root, "gfx", "models", "buildings", "african", "african_city", "african_city_academy_01.mesh")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}

	original, err := mesh.Read(data)
	if err != nil {
		t.Fatal(err)
	}

	source := &original.Shapes[0].Meshes[0]
	piece := model.Convert(source)[0]

	// The OBJ of it, as the plugin writes it: v counted up from the bottom.
	var obj strings.Builder

	for vertex := range piece.Vertices() {
		p, n := piece.Positions[vertex*3:], piece.Normals[vertex*3:]
		obj.WriteString("v " + floatsOf(p[0], p[1], p[2]) + "\nvt " + floatsOf(piece.UV0[vertex*2], 1-piece.UV0[vertex*2+1]) +
			"\nvn " + floatsOf(n[0], n[1], n[2]) + "\n")
	}

	for triangle := 0; triangle+2 < len(piece.Indices); triangle += 3 {
		obj.WriteString("f")

		for corner := range 3 {
			index := itoa(int(piece.Indices[triangle+corner]) + 1)
			obj.WriteString(" " + index + "/" + index + "/" + index)
		}

		obj.WriteString("\n")
	}

	groups, err := ReadOBJ(strings.NewReader(obj.String()))
	if err != nil {
		t.Fatal(err)
	}

	back := Convert(groups[0], 1, Material{})

	if len(back.Positions) != len(source.Positions) || len(back.Indices) != len(source.Indices) {
		t.Fatalf("%d vertices and %d triangles back, want %d and %d",
			len(back.Positions)/3, len(back.Indices)/3, len(source.Positions)/3, len(source.Indices)/3)
	}

	agree := 0

	for vertex := range len(source.Positions) / 3 {
		for axis := range 3 {
			if math.Abs(float64(back.Positions[vertex*3+axis]-source.Positions[vertex*3+axis])) > 1e-5 {
				t.Fatalf("vertex %d is at %v, want %v", vertex, back.Positions[vertex*3:vertex*3+3], source.Positions[vertex*3:vertex*3+3])
			}
		}

		want, got := source.Tangents[vertex*4:vertex*4+4], back.Tangents[vertex*4:vertex*4+4]
		if want[3] == got[3] && want[0]*got[0]+want[1]*got[1]+want[2]*got[2] > 0.9 {
			agree++
		}
	}

	if share := float64(agree) / float64(len(source.Positions)/3); share < 0.98 {
		t.Errorf("%.1f%% of the tangents agree with the exporter's, want 98%% or more", share*100)
	}
}

func floatsOf(values ...float32) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconvFloat(value)
	}

	return strings.Join(parts, " ")
}

func strconvFloat(value float32) string {
	return strconv.FormatFloat(float64(value), 'g', -1, 32)
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
