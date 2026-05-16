package tui

import (
	"image/color"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// animIDCounter provides unique IDs for each Anim instance.
var animIDCounter atomic.Int64

// animInterval is the duration between animation frames (from contracts/parity/parity.json).
var animInterval = func() time.Duration {
	return time.Duration(parityValues.Anim.IntervalMs) * time.Millisecond
}()

// animRunes are the characters used for the cycling animation (from contracts/parity/parity.json).
var animRunes = []rune(parityValues.Anim.Runes)

// AnimStepMsg is sent to advance a specific Anim instance by one frame.
type AnimStepMsg struct {
	ID int
}

// AnimSettings configures a new Anim.
type AnimSettings struct {
	Width       int         // number of cycling chars (default 10)
	Label       string      // text label after the animation
	LabelColor  color.Color // label color (default: nil → no color)
	GradColorA  color.Color // gradient start
	GradColorB  color.Color // gradient end
	CycleColors bool        // shift gradient each frame
}

// Anim is a gradient-cycling character scramble animation component.
// It satisfies the Animatable interface.
type Anim struct {
	id          int
	width       int
	label       string
	gradA       color.Color
	gradB       color.Color
	cycleColors bool

	step          int
	frames        [][]string // pre-rendered cycling char frames [frame][charPos]
	labelRendered string     // pre-rendered label with color

	// Birth stagger.
	startTime    time.Time
	birthOffsets []time.Duration
	initialFrame []string // pre-rendered initial '.' frame
	initialized  bool
}

// NewAnim creates an Anim with the given settings.
// Defaults: Width=10.
func NewAnim(s AnimSettings) Anim {
	if s.Width <= 0 {
		s.Width = 10
	}

	id := int(animIDCounter.Add(1))

	// Determine frame count.
	frameCount := 10
	if s.CycleColors {
		frameCount = s.Width * 2
	}

	// Build gradient colors.
	gradSize := s.Width
	if s.CycleColors {
		gradSize = s.Width * 3
	}
	grad := MakeGradient(gradSize, s.GradColorA, s.GradColorB)

	// Pre-render frames.
	frames := make([][]string, frameCount)
	for f := range frames {
		frame := make([]string, s.Width)
		for i := range frame {
			ch := animRunes[rand.IntN(len(animRunes))]
			ci := i
			if s.CycleColors {
				ci = (i + f) % len(grad)
			}
			style := lipgloss.NewStyle().Foreground(grad[ci])
			frame[i] = style.Render(string(ch))
		}
		frames[f] = frame
	}

	// Pre-render initial '.' frame.
	initFrame := make([]string, s.Width)
	dotStyle := lipgloss.NewStyle().Foreground(s.GradColorA)
	for i := range initFrame {
		initFrame[i] = dotStyle.Render(".")
	}

	// Random birth offsets: 0–1s per char.
	offsets := make([]time.Duration, s.Width)
	for i := range offsets {
		offsets[i] = time.Duration(rand.Int64N(int64(time.Second)))
	}

	// Pre-render label.
	var labelRendered string
	if s.Label != "" {
		if s.LabelColor != nil {
			labelRendered = lipgloss.NewStyle().
				Foreground(s.LabelColor).
				Render(s.Label)
		} else {
			labelRendered = s.Label
		}
	}

	return Anim{
		id:            id,
		width:         s.Width,
		label:         s.Label,
		gradA:         s.GradColorA,
		gradB:         s.GradColorB,
		cycleColors:   s.CycleColors,
		frames:        frames,
		labelRendered: labelRendered,
		birthOffsets:  offsets,
		initialFrame:  initFrame,
	}
}

// Start begins the animation and returns the first tick command.
func (a Anim) Start() tea.Cmd {
	id := a.id
	return tea.Tick(animInterval, func(time.Time) tea.Msg {
		return AnimStepMsg{ID: id}
	})
}

// Animate advances the animation by one frame if msg matches this Anim's ID.
// Returns a copy with updated state and the next tick command.
func (a Anim) Animate(msg AnimStepMsg) (Anim, tea.Cmd) {
	if msg.ID != a.id {
		return a, nil
	}

	// Initialize start time on first animate call.
	if a.startTime.IsZero() {
		a.startTime = time.Now()
	}

	a.step++

	// Check if all births have elapsed.
	if !a.initialized {
		elapsed := time.Since(a.startTime)
		allBorn := true
		for _, off := range a.birthOffsets {
			if elapsed < off {
				allBorn = false
				break
			}
		}
		a.initialized = allBorn
	}

	id := a.id
	cmd := tea.Tick(animInterval, func(time.Time) tea.Msg {
		return AnimStepMsg{ID: id}
	})
	return a, cmd
}

// View renders the current animation frame.
func (a Anim) View() string {
	if len(a.frames) == 0 {
		return ""
	}

	var b strings.Builder
	frameIdx := a.step % len(a.frames)
	frame := a.frames[frameIdx]

	if !a.initialized && !a.startTime.IsZero() {
		elapsed := time.Since(a.startTime)
		for i, ch := range frame {
			if elapsed >= a.birthOffsets[i] {
				b.WriteString(ch)
			} else {
				b.WriteString(a.initialFrame[i])
			}
		}
	} else {
		for _, ch := range frame {
			b.WriteString(ch)
		}
	}

	if a.labelRendered != "" {
		b.WriteString(a.labelRendered)
	}

	return b.String()
}

// SetLabel updates the label text and returns a copy.
func (a Anim) SetLabel(label string) Anim {
	a.label = label
	if label == "" {
		a.labelRendered = ""
	} else if a.gradA != nil {
		a.labelRendered = lipgloss.NewStyle().
			Foreground(a.gradA).
			Render(label)
	} else {
		a.labelRendered = label
	}
	return a
}

// Width returns the total display width (cycling chars + label rune count).
func (a Anim) Width() int {
	return a.width + len([]rune(a.label))
}

// Tick satisfies the Animatable interface. It calls Start().
func (a Anim) Tick() tea.Cmd {
	return a.Start()
}

// MakeGradient blends two colors in HCL space producing size steps.
func MakeGradient(size int, a, b color.Color) []color.Color {
	if size <= 0 {
		return nil
	}
	if size == 1 {
		return []color.Color{a}
	}

	ca := toColorful(a)
	cb := toColorful(b)

	out := make([]color.Color, size)
	for i := range out {
		t := float64(i) / float64(size-1)
		blended := ca.BlendHcl(cb, t)
		r, g, bl, _ := blended.RGBA()
		out[i] = color.RGBA{
			R: uint8(r >> 8),
			G: uint8(g >> 8),
			B: uint8(bl >> 8),
			A: 255,
		}
	}
	return out
}

// toColorful converts an image/color.Color to a go-colorful Color.
func toColorful(c color.Color) colorful.Color {
	if c == nil {
		return colorful.Color{R: 0.5, G: 0.5, B: 0.5}
	}
	r, g, b, _ := c.RGBA()
	return colorful.Color{
		R: float64(r) / 65535.0,
		G: float64(g) / 65535.0,
		B: float64(b) / 65535.0,
	}
}
