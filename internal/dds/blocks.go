package dds

import "math"

// The block encoders fit a line through the colours of a block, the way the
// usual DXT and BC7 encoders start out: the principal axis of the colours,
// with its two ends at the furthest colours along it. The bounding box of the
// block is tried as well, which is better for blocks of two flat colours, and
// whichever of the two is closer to the pixels is kept. That is far short of
// what a dedicated compressor searches through, but leaves no visible blocks
// on the games' textures, and keeps the tool free of anything to install.

// line returns the two ends of the line that best fits a set of points of
// dims components each, ordered so that the first is the low end.
func line(points [][4]float64, dims int) (low, high [4]float64) {
	if len(points) == 0 {
		return
	}

	var mean [4]float64
	for _, point := range points {
		for c := range dims {
			mean[c] += point[c]
		}
	}

	for c := range dims {
		mean[c] /= float64(len(points))
	}

	var covariance [4][4]float64
	for _, point := range points {
		for i := range dims {
			for j := range dims {
				covariance[i][j] += (point[i] - mean[i]) * (point[j] - mean[j])
			}
		}
	}

	// The principal axis, by power iteration from the diagonal of the box.
	var axis [4]float64
	for c := range dims {
		axis[c] = 1
	}

	for range 8 {
		var next [4]float64

		for i := range dims {
			for j := range dims {
				next[i] += covariance[i][j] * axis[j]
			}
		}

		length := 0.0
		for c := range dims {
			length += next[c] * next[c]
		}

		if length < 1e-12 {
			break
		}

		length = math.Sqrt(length)
		for c := range dims {
			axis[c] = next[c] / length
		}
	}

	lowest, highest := math.Inf(1), math.Inf(-1)

	for _, point := range points {
		t := 0.0
		for c := range dims {
			t += (point[c] - mean[c]) * axis[c]
		}

		lowest = min(lowest, t)
		highest = max(highest, t)
	}

	for c := range dims {
		low[c] = clamp255(mean[c] + axis[c]*lowest)
		high[c] = clamp255(mean[c] + axis[c]*highest)
	}

	return low, high
}

// box returns the corners of the box the points fit in.
func box(points [][4]float64, dims int) (low, high [4]float64) {
	for c := range dims {
		low[c], high[c] = 255, 0
	}

	for _, point := range points {
		for c := range dims {
			low[c] = min(low[c], point[c])
			high[c] = max(high[c], point[c])
		}
	}

	return low, high
}

func clamp255(value float64) float64 {
	return min(max(value, 0), 255)
}

func distance(a, b [4]int, dims int) int {
	sum := 0
	for c := range dims {
		d := a[c] - b[c]
		sum += d * d
	}

	return sum
}

func pixel(block *[16][4]uint8, index int) [4]int {
	p := block[index]

	return [4]int{int(p[0]), int(p[1]), int(p[2]), int(p[3])}
}

// The colours of a DXT block are 5:6:5 bits.

func to565(colour [4]float64) uint16 {
	r := uint16((colour[0]*31 + 127.5) / 255)
	g := uint16((colour[1]*63 + 127.5) / 255)
	b := uint16((colour[2]*31 + 127.5) / 255)

	return r<<11 | g<<5 | b
}

func from565(value uint16) [4]int {
	r := int(value>>11) & 31
	g := int(value>>5) & 63
	b := int(value) & 31

	return [4]int{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2, 255}
}

func mix(a, b [4]int, wa, wb, total int) [4]int {
	var out [4]int
	for c := range 4 {
		out[c] = (a[c]*wa + b[c]*wb) / total
	}

	return out
}

// colourBlock is the colour half of a DXT block: two 5:6:5 colours and two
// bits a pixel choosing among them and what lies between them.
type colourBlock struct {
	c0, c1  uint16
	indices uint32
	err     int
}

// fitColours encodes the colours of a block. With transparent set, the block
// is in DXT1's mode of three colours and transparent black, and the pixels
// whose alpha is below half are transparent; otherwise it is in the mode of
// four colours, which DXT5 always is.
func fitColours(block *[16][4]uint8, transparent bool) colourBlock {
	var points [][4]float64

	for index := range 16 {
		p := block[index]
		if transparent && p[3] < 128 {
			continue
		}

		points = append(points, [4]float64{float64(p[0]), float64(p[1]), float64(p[2])})
	}

	if len(points) == 0 {
		return colourBlock{indices: 0xffffffff}
	}

	best := colourBlock{err: math.MaxInt}

	for _, ends := range [][2][4]float64{pair(line(points, 3)), pair(box(points, 3))} {
		candidate := encodeColours(block, to565(ends[1]), to565(ends[0]), transparent)
		if candidate.err < best.err {
			best = candidate
		}
	}

	return best
}

func pair(low, high [4]float64) [2][4]float64 {
	return [2][4]float64{low, high}
}

func encodeColours(block *[16][4]uint8, c0, c1 uint16, transparent bool) colourBlock {
	// The order of the two colours says the mode: the first larger for four
	// colours, not larger for three and transparent.
	if transparent == (c0 > c1) {
		c0, c1 = c1, c0
	}

	p0, p1 := from565(c0), from565(c1)

	var palette [4][4]int

	colours := 4
	if transparent {
		palette = [4][4]int{p0, p1, mix(p0, p1, 1, 1, 2)}
		colours = 3
	} else {
		palette = [4][4]int{p0, p1, mix(p0, p1, 2, 1, 3), mix(p0, p1, 1, 2, 3)}
	}

	encoded := colourBlock{c0: c0, c1: c1}

	for index := range 16 {
		if transparent && block[index][3] < 128 {
			encoded.indices |= 3 << (2 * index)
			continue
		}

		p := pixel(block, index)
		chosen, nearest := 0, math.MaxInt

		for i := range colours {
			if d := distance(p, palette[i], 3); d < nearest {
				chosen, nearest = i, d
			}
		}

		encoded.indices |= uint32(chosen) << (2 * index)
		encoded.err += nearest
	}

	return encoded
}

func (c colourBlock) write(out []byte) {
	out[0], out[1] = byte(c.c0), byte(c.c0>>8)
	out[2], out[3] = byte(c.c1), byte(c.c1>>8)
	out[4], out[5], out[6], out[7] = byte(c.indices), byte(c.indices>>8), byte(c.indices>>16), byte(c.indices>>24)
}

func encodeBC1(block *[16][4]uint8, out []byte) {
	transparent := false
	for index := range 16 {
		if block[index][3] < 128 {
			transparent = true
		}
	}

	fitColours(block, transparent).write(out)
}

func encodeBC3(block *[16][4]uint8, out []byte) {
	encodeAlpha(block, out[0:8])
	fitColours(block, false).write(out[8:16])
}

// encodeAlpha writes the alpha half of a DXT5 block: the two ends, the
// largest first for eight values between them, and three bits a pixel.
func encodeAlpha(block *[16][4]uint8, out []byte) {
	low, high := 255, 0
	for index := range 16 {
		a := int(block[index][3])
		low, high = min(low, a), max(high, a)
	}

	out[0], out[1] = byte(high), byte(low)

	var bitsOut uint64

	if high > low {
		var palette [8]int
		palette[0], palette[1] = high, low

		for i := 1; i < 7; i++ {
			palette[i+1] = ((7-i)*high + i*low) / 7
		}

		for index := range 16 {
			a := int(block[index][3])
			chosen, nearest := 0, math.MaxInt

			for i, value := range palette {
				if d := (a - value) * (a - value); d < nearest {
					chosen, nearest = i, d
				}
			}

			bitsOut |= uint64(chosen) << (3 * index)
		}
	}

	for i := range 6 {
		out[2+i] = byte(bitsOut >> (8 * i))
	}
}

// BC7 is written in its mode 6 only: one line through the colour and alpha
// together, seven bits an end and channel with a bit of each end shared by
// its channels, and four bits a pixel. It is the mode of the most precision
// for a block of one line, and what the fast BC7 encoders lean on.

var bc7Weights = [16]int{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64}

func encodeBC7(block *[16][4]uint8, out []byte) {
	points := make([][4]float64, 16)
	for index := range 16 {
		p := block[index]
		points[index] = [4]float64{float64(p[0]), float64(p[1]), float64(p[2]), float64(p[3])}
	}

	type fitted struct {
		ends    [2][4]int // seven bits a channel
		pbits   [2]int
		indices [16]int
		err     int
	}

	best := fitted{err: math.MaxInt}

	for _, fit := range [][2][4]float64{pair(line(points, 4)), pair(box(points, 4))} {
		// The low bits of the two ends are tried every way round: one end's
		// bit can make up for what the other's rounds away.
		for combination := range 4 {
			var candidate fitted

			var expanded [2][4]int

			for end := range 2 {
				bit := combination >> end & 1
				candidate.ends[end], expanded[end] = quantise7(fit[end], bit)
				candidate.pbits[end] = bit
			}

			for index := range 16 {
				p := pixel(block, index)
				chosen, nearest := 0, math.MaxInt

				for i, weight := range bc7Weights {
					var value [4]int
					for c := range 4 {
						value[c] = ((64-weight)*expanded[0][c] + weight*expanded[1][c] + 32) >> 6
					}

					if d := distance(p, value, 4); d < nearest {
						chosen, nearest = i, d
					}
				}

				candidate.indices[index] = chosen
				candidate.err += nearest
			}

			if candidate.err < best.err {
				best = candidate
			}
		}
	}

	// The first pixel's index is written with three bits, its top bit taken
	// as clear, which swapping the ends makes it.
	if best.indices[0] >= 8 {
		best.ends[0], best.ends[1] = best.ends[1], best.ends[0]
		best.pbits[0], best.pbits[1] = best.pbits[1], best.pbits[0]

		for index := range best.indices {
			best.indices[index] = 15 - best.indices[index]
		}
	}

	var writer bitWriter

	writer.write(1<<6, 7) // mode 6

	for c := range 4 {
		writer.write(uint64(best.ends[0][c]), 7)
		writer.write(uint64(best.ends[1][c]), 7)
	}

	writer.write(uint64(best.pbits[0]), 1)
	writer.write(uint64(best.pbits[1]), 1)

	for index, value := range best.indices {
		if index == 0 {
			writer.write(uint64(value), 3)
		} else {
			writer.write(uint64(value), 4)
		}
	}

	writer.put(out)
}

// quantise7 rounds an end to seven bits a channel under a shared low bit, and
// returns the eight bit value it stands for as well.
func quantise7(end [4]float64, bit int) (quantised, value [4]int) {
	for c := range 4 {
		q := int(math.Round((end[c] - float64(bit)) / 2))
		q = min(max(q, 0), 127)
		quantised[c] = q
		value[c] = q<<1 | bit
	}

	return quantised, value
}

// bitWriter packs the fields of a BC7 block from its lowest bit up.
type bitWriter struct {
	low, high uint64
	at        uint
}

func (w *bitWriter) write(value uint64, count uint) {
	for i := range count {
		bit := (value >> i) & 1
		if w.at < 64 {
			w.low |= bit << w.at
		} else {
			w.high |= bit << (w.at - 64)
		}

		w.at++
	}
}

func (w *bitWriter) put(out []byte) {
	for i := range 8 {
		out[i] = byte(w.low >> (8 * i))
		out[8+i] = byte(w.high >> (8 * i))
	}
}
