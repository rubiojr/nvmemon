package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rubiojr/nvmemon/internal/monitor"
)

func TestNormTemp(t *testing.T) {
	assert.InDelta(t, 0.0, normTemp(scaleMinC), 1e-9)
	assert.InDelta(t, 1.0, normTemp(scaleMaxC), 1e-9)
	assert.InDelta(t, 0.0, normTemp(0), 1e-9)   // clamped low
	assert.InDelta(t, 1.0, normTemp(150), 1e-9) // clamped high
	mid := normTemp((scaleMinC + scaleMaxC) / 2)
	assert.InDelta(t, 0.5, mid, 1e-9)
}

func TestClamp01(t *testing.T) {
	assert.Equal(t, 0.0, clamp01(-1))
	assert.Equal(t, 1.0, clamp01(2))
	assert.Equal(t, 0.5, clamp01(0.5))
	assert.Equal(t, 0.0, clamp01(nan()))
}

func nan() float64 {
	z := 0.0
	return z / z
}

// warmth measures how "hot" a color reads (red minus blue channel).
func warmth(c interface{ RGBA() (r, g, b, a uint32) }) float64 {
	r, _, b, _ := c.RGBA()
	return float64(r) - float64(b)
}

// As temperature rises from cold to the hot band the warmth (red over blue)
// should trend upward. We sample coarsely because Lab blending through the
// green/yellow midtones is not strictly channel-monotonic.
func TestColorRampWarmthTrend(t *testing.T) {
	cold := warmth(colorAt(0.0))
	cool := warmth(colorAt(0.3))
	warm := warmth(colorAt(0.6))
	hot := warmth(colorAt(1.0))
	assert.Less(t, cold, cool, "cool should be warmer than cold")
	assert.Less(t, cool, warm, "warm should be warmer than cool")
	assert.Less(t, cold, hot, "hot end should be much warmer than cold end")
}

func TestColorAtEndpoints(t *testing.T) {
	cold := colorAt(0)
	hot := colorAt(1)
	// Cold endpoint is bluish: blue dominates red.
	assert.Greater(t, cold.B, cold.R)
	// Hot endpoint is reddish: red dominates blue.
	assert.Greater(t, hot.R, hot.B)
}

// countRunes counts a rune ignoring ANSI escape styling.
func countRunes(s, r string) int {
	return strings.Count(s, r)
}

func TestRenderBarFill(t *testing.T) {
	width := 20

	// Below the scale floor -> empty.
	low := renderBar(scaleMinC-5, width)
	assert.Equal(t, 0, countRunes(low, barFull))
	assert.Equal(t, width, countRunes(low, barEmpty))

	// At/above the scale ceiling -> full.
	high := renderBar(scaleMaxC+5, width)
	assert.Equal(t, width, countRunes(high, barFull))
	assert.Equal(t, 0, countRunes(high, barEmpty))

	// Midpoint -> roughly half filled.
	mid := renderBar((scaleMinC+scaleMaxC)/2, width)
	filled := countRunes(mid, barFull)
	assert.InDelta(t, width/2, filled, 1)
}

func TestRenderBarMinWidth(t *testing.T) {
	out := renderBar(50, 0)
	// width is clamped to at least 1, so exactly one cell is rendered.
	assert.Equal(t, 1, countRunes(out, barFull)+countRunes(out, barEmpty))
}

func TestRenderThrottleStates(t *testing.T) {
	// No throttle data.
	noData := renderThrottle(monitor.Drive{})
	assert.Contains(t, noData, "unavailable")

	// Clean drive.
	clean := renderThrottle(monitor.Drive{Throttle: &monitor.ThrottleStats{}})
	assert.Contains(t, clean, "no thermal throttling")

	// Throttled drive.
	hot := renderThrottle(monitor.Drive{Throttle: &monitor.ThrottleStats{
		Therm1TransCount: 2, Therm1TotalTime: 30, WarningTempTime: 5,
	}})
	assert.Contains(t, hot, "throttled")
	assert.Contains(t, hot, "light×2")
}

func TestRenderDriveContainsKeyInfo(t *testing.T) {
	m := NewModel(nil, 0)
	m.width = 100
	d := monitor.Drive{
		Name:    "nvme1",
		Model:   "USB4 Enclosure",
		Address: "0000:03:00.0",
		Sensors: []monitor.TempSensor{
			{Label: "Composite", Celsius: 32.9, WarnC: 81.8, CritC: 84.8},
		},
		Throttle: &monitor.ThrottleStats{},
	}
	out := m.renderDrive(d)
	assert.Contains(t, out, "nvme1")
	assert.Contains(t, out, "USB4 Enclosure")
	assert.Contains(t, out, "0000:03:00.0")
	assert.Contains(t, out, "32.9")
}

func TestBarWidthBounds(t *testing.T) {
	require.LessOrEqual(t, NewModelWidth(0).barWidth(), 48)
	require.GreaterOrEqual(t, NewModelWidth(0).barWidth(), 10)
	require.LessOrEqual(t, NewModelWidth(10000).barWidth(), 48)
}

// NewModelWidth is a tiny test helper to build a Model with a fixed width.
func NewModelWidth(w int) Model {
	m := NewModel(nil, 0)
	m.width = w
	return m
}

func TestDeltaMBps(t *testing.T) {
	// 100 MB over 2s = 50 MB/s.
	assert.InDelta(t, 50.0, deltaMBps(100_000_000, 0, 2), 1e-9)
	// Counter reset / wrap -> 0, not negative.
	assert.Equal(t, 0.0, deltaMBps(0, 100, 1))
	// Non-positive dt -> 0.
	assert.Equal(t, 0.0, deltaMBps(100, 0, 0))
}

func TestClampPct(t *testing.T) {
	assert.Equal(t, 0.0, clampPct(-5))
	assert.Equal(t, 100.0, clampPct(150))
	assert.Equal(t, 42.0, clampPct(42))
}

func TestComputeRates(t *testing.T) {
	m := NewModel(nil, 0)
	t0 := time.Now()

	prev := &monitor.Snapshot{Drives: []monitor.Drive{{
		Name: "nvme0",
		IO:   &monitor.IOCounters{ReadSectors: 0, WriteSectors: 0, IoMillis: 0},
	}}}
	// First snapshot: no history, rate invalid.
	m.computeRates(prev, t0)
	m.snap = prev
	m.updated = t0

	// Second snapshot two seconds later: +200000 read sectors (~102.4 MB/s),
	// +100000 write sectors, +1000 io_ms over 2000ms -> 50% busy.
	next := &monitor.Snapshot{Drives: []monitor.Drive{{
		Name: "nvme0",
		IO:   &monitor.IOCounters{ReadSectors: 200_000, WriteSectors: 100_000, IoMillis: 1000},
	}}}
	m.computeRates(next, t0.Add(2*time.Second))

	r := m.rates["nvme0"]
	require.True(t, r.valid)
	assert.InDelta(t, 200_000*512/1e6/2, r.readMBps, 0.01)
	assert.InDelta(t, 100_000*512/1e6/2, r.writeMBps, 0.01)
	assert.InDelta(t, 50.0, r.utilPct, 0.01)
	assert.Greater(t, m.peakMBps["nvme0"], 0.0)
}

func TestComputeRatesFirstSampleInvalid(t *testing.T) {
	m := NewModel(nil, 0)
	snap := &monitor.Snapshot{Drives: []monitor.Drive{{
		Name: "nvme0", IO: &monitor.IOCounters{ReadSectors: 5},
	}}}
	m.computeRates(snap, time.Now())
	assert.False(t, m.rates["nvme0"].valid)
}

func TestHumanBytes(t *testing.T) {
	assert.Equal(t, "512B", humanBytes(512))
	assert.Equal(t, "1.0K", humanBytes(1024))
	assert.Equal(t, "472G", humanBytes(472*1<<30))
	assert.Equal(t, "1.0T", humanBytes(1<<40))
	assert.Equal(t, "2.0P", humanBytes(2<<50))
}

func TestLoadColorAtEndpoints(t *testing.T) {
	low := loadColorAt(0)
	high := loadColorAt(1)
	// Low end is green (green channel dominant), high end is red.
	assert.Greater(t, low.G, low.R)
	assert.Greater(t, high.R, high.G)
}

func TestRenderActivityStates(t *testing.T) {
	m := NewModel(nil, 0)
	m.width = 100

	// No rate history yet -> "measuring".
	d := monitor.Drive{Name: "nvme0", IO: &monitor.IOCounters{}}
	out := strings.Join(m.renderActivity(d, 8, 20), "\n")
	assert.Contains(t, out, "measuring")
	assert.Contains(t, out, "no mounted filesystem")

	// With rates + capacity.
	m.rates["nvme0"] = ioRate{valid: true, readMBps: 100, writeMBps: 10, utilPct: 30}
	m.peakMBps["nvme0"] = 200
	d.Capacity = &monitor.Capacity{UsedBytes: 50 * 1 << 30, TotalBytes: 100 * 1 << 30}
	out = strings.Join(m.renderActivity(d, 8, 20), "\n")
	assert.Contains(t, out, "100.0")
	assert.Contains(t, out, "MB/s")
	assert.Contains(t, out, "busy")
	assert.Contains(t, out, "50%")
	assert.Contains(t, out, "50.0G / 100G")
}
