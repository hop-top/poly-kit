package qmochi

// BrailleCanvas provides a high-resolution canvas using Braille characters.
// Each cell represents a 2x4 grid of "pixels".
type BrailleCanvas struct {
	width  int
	height int
	grid   [][]byte
}

// NewBrailleCanvas creates a new Braille canvas.
func NewBrailleCanvas(width, height int) *BrailleCanvas {
	grid := make([][]byte, height)
	for i := range grid {
		grid[i] = make([]byte, width)
	}
	return &BrailleCanvas{
		width:  width,
		height: height,
		grid:   grid,
	}
}

// Set sets a "pixel" at (x, y).
// x: [0, width*2), y: [0, height*4)
func (c *BrailleCanvas) Set(x, y int) {
	if x < 0 || x >= c.width*2 || y < 0 || y >= c.height*4 {
		return
	}

	cellX := x / 2
	cellY := y / 4

	dotX := x % 2
	dotY := y % 4

	// Braille dot mapping:
	// 1 4
	// 2 5
	// 3 6
	// 7 8
	// Row: 0 1 2 3
	// Col: 0 1

	shift := uint(0)
	switch {
	case dotX == 0 && dotY == 0:
		shift = 0
	case dotX == 0 && dotY == 1:
		shift = 1
	case dotX == 0 && dotY == 2:
		shift = 2
	case dotX == 1 && dotY == 0:
		shift = 3
	case dotX == 1 && dotY == 1:
		shift = 4
	case dotX == 1 && dotY == 2:
		shift = 5
	case dotX == 0 && dotY == 3:
		shift = 6
	case dotX == 1 && dotY == 3:
		shift = 7
	}

	c.grid[cellY][cellX] |= (1 << shift)
}

// Render returns the canvas as a string.
func (c *BrailleCanvas) Render() string {
	var s string
	for y := 0; y < c.height; y++ {
		for x := 0; x < c.width; x++ {
			// Braille characters start at U+2800
			r := rune(0x2800 + int(c.grid[y][x]))
			s += string(r)
		}
		s += "\n"
	}
	return s
}
