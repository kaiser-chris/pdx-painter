package painter

import (
	"image"
	"math"
)

// The games share textures between models: a sheet of a building's walls is
// laid on several buildings, an atlas of decals on a dozen, each by a part of
// it. Painter only paints where the mesh it has lies, and pads the rest of
// what it exports from the edges of that. Laid over the whole map, that would
// wipe out what the other models use, so packing takes from Painter only the
// texels the mesh covers, and a margin around them for the mipmaps to blend
// into, and keeps the rest of the map as it was.

// coverageMargin is how many texels around the triangles count as covered.
// The smaller mipmap levels average texels the triangles do not cover into
// those they do, so the margin is what keeps the edge of an island in the
// colours Painter gave it.
const coverageMargin = 4

// coverage marks the texels of a size the triangles of a group's parts cover
// in texture space, with a margin.
func coverage(g *partGroup, width, height int) *image.Gray {
	mask := image.NewGray(image.Rect(0, 0, width, height))

	for _, part := range g.parts {
		for index := range part.Pieces {
			piece := &part.Pieces[index]

			uv := piece.UV0
			if g.uvSet == 1 {
				uv = piece.UV1
			}

			for t := 0; t+2 < len(piece.Indices); t += 3 {
				var corners [3][2]float64

				for c := range 3 {
					v := int(piece.Indices[t+c])
					corners[c] = [2]float64{float64(uv[v*2]) * float64(width), float64(uv[v*2+1]) * float64(height)}
				}

				fill(mask, corners)
			}
		}
	}

	return dilate(mask, coverageMargin)
}

// fill marks the texels whose centres a triangle covers, and those its
// corners are in, so that a triangle thinner than a texel still marks it. A
// triangle beyond the texture, as a tiling one is, is wrapped back into it,
// the way the texture repeats.
func fill(mask *image.Gray, corners [3][2]float64) {
	width, height := mask.Rect.Dx(), mask.Rect.Dy()

	lowX := math.Floor(min(corners[0][0], corners[1][0], corners[2][0]))
	lowY := math.Floor(min(corners[0][1], corners[1][1], corners[2][1]))
	highX := math.Ceil(max(corners[0][0], corners[1][0], corners[2][0]))
	highY := math.Ceil(max(corners[0][1], corners[1][1], corners[2][1]))

	// A triangle wider than the texture several times over covers all of it.
	if highX-lowX > float64(width)*2 || highY-lowY > float64(height)*2 {
		for i := range mask.Pix {
			mask.Pix[i] = 255
		}

		return
	}

	mark := func(x, y int) {
		x = ((x % width) + width) % width
		y = ((y % height) + height) % height
		mask.Pix[y*mask.Stride+x] = 255
	}

	for _, corner := range corners {
		mark(int(math.Floor(corner[0])), int(math.Floor(corner[1])))
	}

	edge := func(a, b [2]float64, x, y float64) float64 {
		return (b[0]-a[0])*(y-a[1]) - (b[1]-a[1])*(x-a[0])
	}

	area := edge(corners[0], corners[1], corners[2][0], corners[2][1])
	if area == 0 {
		return
	}

	for y := int(lowY); y < int(highY); y++ {
		for x := int(lowX); x < int(highX); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5

			w0 := edge(corners[1], corners[2], px, py)
			w1 := edge(corners[2], corners[0], px, py)
			w2 := edge(corners[0], corners[1], px, py)

			if area > 0 && w0 >= 0 && w1 >= 0 && w2 >= 0 || area < 0 && w0 <= 0 && w1 <= 0 && w2 <= 0 {
				mark(x, y)
			}
		}
	}
}

// dilate widens what a mask marks by a number of texels in every direction,
// a square around each.
func dilate(mask *image.Gray, by int) *image.Gray {
	width, height := mask.Rect.Dx(), mask.Rect.Dy()

	// Across, then down: two passes of a line make the square.
	across := image.NewGray(mask.Rect)

	for y := range height {
		for x := range width {
			for d := -by; d <= by; d++ {
				if sx := x + d; sx >= 0 && sx < width && mask.Pix[y*mask.Stride+sx] != 0 {
					across.Pix[y*across.Stride+x] = 255

					break
				}
			}
		}
	}

	out := image.NewGray(mask.Rect)

	for y := range height {
		for x := range width {
			for d := -by; d <= by; d++ {
				if sy := y + d; sy >= 0 && sy < height && across.Pix[sy*across.Stride+x] != 0 {
					out.Pix[y*out.Stride+x] = 255

					break
				}
			}
		}
	}

	return out
}

// covered reports whether a mask marks a texel of a map of another size,
// scaled to it.
func covered(mask *image.Gray, x, y, width, height int) bool {
	mx := x * mask.Rect.Dx() / width
	my := y * mask.Rect.Dy() / height

	return mask.Pix[my*mask.Stride+mx] != 0
}
