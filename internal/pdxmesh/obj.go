package pdxmesh

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Group is the geometry of one material of an OBJ file, in its own right
// handed coordinates, with a vertex for every corner of a face that differs
// from the others in position, texture coordinates or normal.
type Group struct {
	Material string

	// Positions and Normals hold three numbers a vertex, UV two, and Indices
	// three vertex numbers a triangle, wound anticlockwise.
	Positions, Normals, UV []float32
	Indices                []int32
}

// ReadOBJ reads the faces of an OBJ file, by the material each is drawn with,
// in the order the materials first come. A face of more than three corners is
// cut into a fan of triangles. A corner without a normal is given the normal
// of its face; one without texture coordinates gets zeros.
func ReadOBJ(r io.Reader) ([]Group, error) {
	var (
		positions, uvs, normals [][3]float64
		groups                  []*Group
		current                 *Group
	)

	byName := map[string]*Group{}
	corners := map[*Group]map[[3]int]int32{}

	use := func(material string) {
		if group, ok := byName[material]; ok {
			current = group

			return
		}

		current = &Group{Material: material}
		byName[material] = current
		corners[current] = map[[3]int]int32{}
		groups = append(groups, current)
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)

	for number := 1; scanner.Scan(); number++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		switch fields[0] {
		case "v", "vt", "vn":
			values, err := numbers(fields[1:])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", number, err)
			}

			switch fields[0] {
			case "v":
				positions = append(positions, values)
			case "vt":
				uvs = append(uvs, values)
			case "vn":
				normals = append(normals, values)
			}

		case "usemtl":
			use(strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "usemtl")))

		case "f":
			if current == nil {
				use("")
			}

			if len(fields) < 4 {
				return nil, fmt.Errorf("line %d: a face of %d corners", number, len(fields)-1)
			}

			face := make([][3]int, len(fields)-1)
			for index, corner := range fields[1:] {
				refs, err := cornerOf(corner, len(positions), len(uvs), len(normals))
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", number, err)
				}

				face[index] = refs
			}

			faceNormal := normalOf(positions, face)

			vertex := func(refs [3]int) int32 {
				if at, ok := corners[current][refs]; ok {
					return at
				}

				at := int32(len(current.Positions) / 3)
				corners[current][refs] = at

				p := positions[refs[0]]
				current.Positions = append(current.Positions, float32(p[0]), float32(p[1]), float32(p[2]))

				uv := [3]float64{}
				if refs[1] >= 0 {
					uv = uvs[refs[1]]
				}

				current.UV = append(current.UV, float32(uv[0]), float32(uv[1]))

				n := faceNormal
				if refs[2] >= 0 {
					n = normals[refs[2]]
				}

				current.Normals = append(current.Normals, float32(n[0]), float32(n[1]), float32(n[2]))

				return at
			}

			// A corner without a normal takes its face's, which no other
			// face shares: it is its own vertex.
			for index := range face {
				if face[index][2] < 0 {
					face[index][2] = -2 - len(current.Indices)
				}
			}

			first := vertex(face[0])
			for index := 1; index+1 < len(face); index++ {
				current.Indices = append(current.Indices, first, vertex(face[index]), vertex(face[index+1]))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var read []Group

	for _, group := range groups {
		if len(group.Indices) > 0 {
			read = append(read, *group)
		}
	}

	return read, nil
}

func numbers(fields []string) ([3]float64, error) {
	var values [3]float64

	for index := range min(len(fields), 3) {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err != nil {
			return values, err
		}

		values[index] = value
	}

	return values, nil
}

// cornerOf reads a corner of a face, v, v/vt, v//vn or v/vt/vn, counting from
// 1, or back from the last for a negative number, into indices counting from
// 0, -1 for what it leaves out.
func cornerOf(corner string, positions, uvs, normals int) ([3]int, error) {
	refs := [3]int{-1, -1, -1}
	counts := [3]int{positions, uvs, normals}

	for index, part := range strings.SplitN(corner, "/", 3) {
		if part == "" {
			continue
		}

		value, err := strconv.Atoi(part)
		if err != nil {
			return refs, fmt.Errorf("corner %q: %w", corner, err)
		}

		if value < 0 {
			value += counts[index] + 1
		}

		if value < 1 || value > counts[index] {
			return refs, fmt.Errorf("corner %q names a vertex that is not there", corner)
		}

		refs[index] = value - 1
	}

	if refs[0] < 0 {
		return refs, fmt.Errorf("corner %q has no position", corner)
	}

	return refs, nil
}

// normalOf is the normal of a face, from its first three corners.
func normalOf(positions [][3]float64, face [][3]int) [3]float64 {
	a, b, c := positions[face[0][0]], positions[face[1][0]], positions[face[2][0]]
	n := cross(sub(b, a), sub(c, a))

	return normalised(n)
}

// Convert turns a group of an OBJ file into a mesh in the games' coordinates,
// its positions scaled, its tangents worked out, drawn with a material.
func Convert(group Group, scale float64, material Material) Mesh {
	vertices := len(group.Positions) / 3

	m := Mesh{
		Positions: make([]float32, len(group.Positions)),
		Normals:   make([]float32, len(group.Normals)),
		UV0:       make([]float32, len(group.UV)),
		Indices:   make([]int32, len(group.Indices)),
		Material:  material,
	}

	for vertex := range vertices {
		for axis := range 3 {
			sign := 1.0
			if axis == 0 {
				sign = -1
			}

			m.Positions[vertex*3+axis] = float32(float64(group.Positions[vertex*3+axis]) * scale * sign)
			m.Normals[vertex*3+axis] = float32(float64(group.Normals[vertex*3+axis]) * sign)
		}

		n := normalised([3]float64{float64(m.Normals[vertex*3]), float64(m.Normals[vertex*3+1]), float64(m.Normals[vertex*3+2])})
		m.Normals[vertex*3], m.Normals[vertex*3+1], m.Normals[vertex*3+2] = float32(n[0]), float32(n[1]), float32(n[2])

		m.UV0[vertex*2] = group.UV[vertex*2]
		m.UV0[vertex*2+1] = 1 - group.UV[vertex*2+1]
	}

	for triangle := 0; triangle+2 < len(group.Indices); triangle += 3 {
		m.Indices[triangle] = group.Indices[triangle]
		m.Indices[triangle+1] = group.Indices[triangle+2]
		m.Indices[triangle+2] = group.Indices[triangle+1]
	}

	m.Tangents = tangents(m.Positions, m.Normals, m.UV0, m.Indices)

	return m
}

// tangents works out the tangent of every vertex: the direction u grows in,
// across its triangles, made square to its normal, and in w which way round
// the direction v grows in is from the cross product of normal and tangent.
func tangents(positions, normals, uv []float32, indices []int32) []float32 {
	vertices := len(positions) / 3
	along := make([][3]float64, vertices)
	up := make([][3]float64, vertices)

	point := func(at int32) [3]float64 {
		return [3]float64{float64(positions[at*3]), float64(positions[at*3+1]), float64(positions[at*3+2])}
	}

	for triangle := 0; triangle+2 < len(indices); triangle += 3 {
		a, b, c := indices[triangle], indices[triangle+1], indices[triangle+2]

		e1, e2 := sub(point(b), point(a)), sub(point(c), point(a))
		du1, dv1 := float64(uv[b*2]-uv[a*2]), float64(uv[b*2+1]-uv[a*2+1])
		du2, dv2 := float64(uv[c*2]-uv[a*2]), float64(uv[c*2+1]-uv[a*2+1])

		determinant := du1*dv2 - du2*dv1
		if math.Abs(determinant) < 1e-12 {
			continue
		}

		r := 1 / determinant
		s := scaled(sub(scaled(e1, dv2), scaled(e2, dv1)), r)
		t := scaled(sub(scaled(e2, du1), scaled(e1, du2)), r)

		for _, corner := range []int32{a, b, c} {
			along[corner] = add(along[corner], s)
			up[corner] = add(up[corner], t)
		}
	}

	out := make([]float32, vertices*4)

	for vertex := range vertices {
		n := [3]float64{float64(normals[vertex*3]), float64(normals[vertex*3+1]), float64(normals[vertex*3+2])}
		t := normalised(sub(along[vertex], scaled(n, dot(n, along[vertex]))))

		// A vertex of no area in texture space gets any tangent square to
		// its normal.
		if dot(t, t) == 0 {
			t = normalised(cross(n, [3]float64{0, 1, 0}))
			if dot(t, t) == 0 {
				t = [3]float64{1, 0, 0}
			}
		}

		w := 1.0
		if dot(cross(n, t), up[vertex]) < 0 {
			w = -1
		}

		out[vertex*4], out[vertex*4+1], out[vertex*4+2], out[vertex*4+3] = float32(t[0]), float32(t[1]), float32(t[2]), float32(w)
	}

	return out
}

func add(a, b [3]float64) [3]float64 { return [3]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func sub(a, b [3]float64) [3]float64 { return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func scaled(a [3]float64, s float64) [3]float64 { return [3]float64{a[0] * s, a[1] * s, a[2] * s} }

func dot(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func normalised(a [3]float64) [3]float64 {
	length := math.Sqrt(dot(a, a))
	if length == 0 {
		return a
	}

	return scaled(a, 1/length)
}
