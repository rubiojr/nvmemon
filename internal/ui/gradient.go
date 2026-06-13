// Package ui renders the nvmemon terminal interface.
package ui

import (
	"math"

	"github.com/lucasb-eyer/go-colorful"
)

// gradientStop is a control point on the temperature color scale.
type gradientStop struct {
	pos   float64 // normalized position in [0,1]
	color colorful.Color
}

// tempPalette defines the cool->hot color ramp used for temperature bars.
// Positions are normalized across the display range (see normTemp).
var tempPalette = mustStops([]struct {
	pos float64
	hex string
}{
	{0.00, "#3aa0ff"}, // cold blue
	{0.30, "#2ecc71"}, // green (comfortable)
	{0.55, "#f1c40f"}, // yellow (warm)
	{0.78, "#e67e22"}, // orange (hot)
	{1.00, "#e74c3c"}, // red (critical)
})

func mustStops(in []struct {
	pos float64
	hex string
}) []gradientStop {
	stops := make([]gradientStop, len(in))
	for i, s := range in {
		c, err := colorful.Hex(s.hex)
		if err != nil {
			panic("ui: invalid palette color " + s.hex)
		}
		stops[i] = gradientStop{pos: s.pos, color: c}
	}
	return stops
}

const (
	// scaleMinC and scaleMaxC bound the temperature color scale. They are
	// chosen so typical NVMe operating temperatures land in the green/yellow
	// band and only genuinely hot drives reach orange/red.
	scaleMinC = 25.0
	scaleMaxC = 90.0
)

// normTemp maps a Celsius value into [0,1] across the display scale.
func normTemp(c float64) float64 {
	return clamp01((c - scaleMinC) / (scaleMaxC - scaleMinC))
}

// loadPalette is a green->red ramp for "load" style metrics (utilization,
// capacity used) where low is calm and high is alarming.
var loadPalette = mustStops([]struct {
	pos float64
	hex string
}{
	{0.00, "#2ecc71"}, // green (idle / empty)
	{0.55, "#f1c40f"}, // yellow
	{0.80, "#e67e22"}, // orange
	{1.00, "#e74c3c"}, // red (saturated / full)
})

// colorAt returns the gradient color at normalized position t in [0,1],
// blending between palette stops in L*a*b* space for smooth transitions.
func colorAt(t float64) colorful.Color {
	return blendStops(tempPalette, t)
}

// loadColorAt returns the load-ramp color at normalized position t in [0,1].
func loadColorAt(t float64) colorful.Color {
	return blendStops(loadPalette, t)
}

// blendStops evaluates a palette at normalized position t in [0,1].
func blendStops(stops []gradientStop, t float64) colorful.Color {
	t = clamp01(t)
	if t <= stops[0].pos {
		return stops[0].color
	}
	if t >= stops[len(stops)-1].pos {
		return stops[len(stops)-1].color
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].pos {
			lo, hi := stops[i-1], stops[i]
			span := hi.pos - lo.pos
			local := 0.0
			if span > 0 {
				local = (t - lo.pos) / span
			}
			return lo.color.BlendLab(hi.color, local).Clamped()
		}
	}
	return stops[len(stops)-1].color
}

// tempColor returns the gradient color representing a Celsius temperature.
func tempColor(c float64) colorful.Color {
	return colorAt(normTemp(c))
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}
