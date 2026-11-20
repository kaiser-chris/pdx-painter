// Package pdxmesh writes the binary .mesh files of the Paradox games, from the
// OBJ files Substance 3D Painter exports its meshes as.
//
// A mesh file holds an object of shapes, each one mesh or more with its
// positions, normals, tangents, texture coordinates and triangles, its
// bounding box and sphere, and a material naming its shader and textures, as
// the games' exporters write them. pdx-asset-go's mesh package reads them; this
// writes static ones, of one level of detail, with no skeleton.
//
// # Coordinates
//
// OBJ is right handed, with the front of a triangle wound anticlockwise and
// its texture coordinates counted up from the bottom. The games compose their
// scenes the way Direct3D does: left handed, and their textures counted down
// from the top. Converting mirrors x, winds every triangle the other way round
// and turns v upside down, which is the reverse of what pdx-asset-go's
// model.Convert does to draw a mesh.
//
// The tangents are worked out in the games' coordinates, with the bitangent's
// sign in w, as the games' exporters work them out: the same rule gives back
// the tangents of 99.7% of the vertices of Victoria 3's shipped buildings,
// bodies and units.
package pdxmesh

import (
	"math"

	"github.com/kaiser-chris/pdx-asset-go/mesh"
)

// Mesh is one mesh of a shape, in the games' coordinates.
type Mesh struct {
	// Positions and Normals hold three numbers a vertex, Tangents four, the
	// last the sign of the bitangent, and UV0 two.
	Positions, Normals, Tangents, UV0 []float32

	// Indices holds three vertex numbers a triangle.
	Indices []int32

	Material Material
}

// Material is what a mesh is drawn with, as the mesh file names it. An asset's
// mesh settings name the textures the games draw it with; these are what the
// mesh was exported with.
type Material struct {
	Shader, Diffuse, Normal, Specular string
}

// Shape is a shape of a mesh file: a name and its meshes.
type Shape struct {
	Name   string
	Meshes []Mesh
}

// MaxNameLength is how long a shape's name may be: the games' exporters cut
// theirs off at it.
const MaxNameLength = 63

// Write writes shapes as a mesh file, each of the most detailed level, laid
// out by pdx-asset-go's mesh.Encode.
func Write(shapes []Shape) ([]byte, error) {
	object := &mesh.Object{Name: "object"}

	for _, shape := range shapes {
		shapeObject := &mesh.Object{Name: shape.Name, Properties: []mesh.Property{ints("lod", 0)}}

		for _, m := range shape.Meshes {
			low, high := bounds(m.Positions)
			centre, radius := sphere(m.Positions, low, high)

			material := &mesh.Object{Name: "material", Properties: []mesh.Property{texts("shader", m.Material.Shader)}}

			for _, texture := range []struct{ name, file string }{
				{"diff", m.Material.Diffuse}, {"n", m.Material.Normal}, {"spec", m.Material.Specular},
			} {
				if texture.file != "" {
					material.Properties = append(material.Properties, texts(texture.name, texture.file))
				}
			}

			shapeObject.Children = append(shapeObject.Children, &mesh.Object{
				Name: "mesh",
				Properties: []mesh.Property{
					floats("p", m.Positions...),
					floats("n", m.Normals...),
					floats("ta", m.Tangents...),
					floats("u0", m.UV0...),
					ints("tri", m.Indices...),
					floats("boundingsphere", centre[0], centre[1], centre[2], radius),
				},
				Children: []*mesh.Object{
					{Name: "aabb", Properties: []mesh.Property{floats("min", low[:]...), floats("max", high[:]...)}},
					material,
				},
			})
		}

		object.Children = append(object.Children, shapeObject)
	}

	return mesh.Encode(&mesh.Object{
		Properties: []mesh.Property{ints("pdxasset", 1, 0)},
		Children:   []*mesh.Object{object},
	})
}

func floats(name string, values ...float32) mesh.Property {
	return mesh.Property{Name: name, Kind: mesh.Floats, Floats: values}
}

func ints(name string, values ...int32) mesh.Property {
	return mesh.Property{Name: name, Kind: mesh.Ints, Ints: values}
}

func texts(name string, values ...string) mesh.Property {
	return mesh.Property{Name: name, Kind: mesh.Strings, Strings: values}
}

func bounds(positions []float32) (low, high [3]float32) {
	if len(positions) < 3 {
		return low, high
	}

	low = [3]float32{positions[0], positions[1], positions[2]}
	high = low

	for index, value := range positions {
		low[index%3] = min(low[index%3], value)
		high[index%3] = max(high[index%3], value)
	}

	return low, high
}

// sphere is a sphere around every position: centred on the box they fit in,
// as large as the furthest of them from there.
func sphere(positions []float32, low, high [3]float32) ([3]float32, float32) {
	centre := [3]float32{(low[0] + high[0]) / 2, (low[1] + high[1]) / 2, (low[2] + high[2]) / 2}

	var furthest float64

	for at := 0; at+2 < len(positions); at += 3 {
		dx := float64(positions[at] - centre[0])
		dy := float64(positions[at+1] - centre[1])
		dz := float64(positions[at+2] - centre[2])
		furthest = max(furthest, dx*dx+dy*dy+dz*dz)
	}

	return centre, float32(math.Sqrt(furthest))
}
