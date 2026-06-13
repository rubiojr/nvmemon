package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

const (
	barFull  = "█"
	barEmpty = "░"
)

var trackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a"))

// renderBarCore draws a horizontal bar of the given width filled to fillFrac
// in [0,1]. Each filled cell is colored by colorFn evaluated at that cell's
// own position, so the bar itself shows a gradient as it grows.
func renderBarCore(fillFrac float64, width int, colorFn func(pos float64) colorful.Color) string {
	if width < 1 {
		width = 1
	}
	fillFrac = clamp01(fillFrac)
	filled := int(fillFrac*float64(width) + 0.5)
	if filled > width {
		filled = width
	}

	var b strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			pos := (float64(i) + 0.5) / float64(width)
			b.WriteString(lipgloss.NewStyle().Foreground(colorFn(pos)).Render(barFull))
		} else {
			b.WriteString(trackStyle.Render(barEmpty))
		}
	}
	return b.String()
}

// renderBar draws a thermometer bar filled to where value sits on the
// temperature scale, colored with the cool->hot temperature gradient.
func renderBar(value float64, width int) string {
	return renderBarCore(normTemp(value), width, colorAt)
}

// renderPercentBar draws a 0..100% bar using the load gradient (green->red).
func renderPercentBar(pct float64, width int) string {
	return renderBarCore(pct/100, width, loadColorAt)
}

// renderAccentBar draws a bar filled to fillFrac (0..1) using a single accent
// color. Useful for unbounded metrics scaled against a rolling peak.
func renderAccentBar(fillFrac float64, width int, hex string) string {
	c, _ := colorful.Hex(hex)
	return renderBarCore(fillFrac, width, func(float64) colorful.Color { return c })
}

// tempLabel renders a Celsius reading colored by its position on the scale.
func tempLabel(text string, c float64) string {
	return lipgloss.NewStyle().
		Foreground(tempColor(c)).
		Bold(true).
		Render(text)
}
