package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
	out := m.renderDrive(d, false)
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

	// No rate history yet -> "measuring"; no capacity -> "capacity unknown".
	d := monitor.Drive{Name: "nvme0", IO: &monitor.IOCounters{}}
	out := strings.Join(m.renderActivity(d, 8, 20), "\n")
	assert.Contains(t, out, "measuring")
	assert.Contains(t, out, "capacity unknown")

	// Known size but nothing mounted -> show size + hint.
	d.Capacity = &monitor.Capacity{TotalBytes: 1 << 40, UsedKnown: false}
	out = strings.Join(m.renderActivity(d, 8, 20), "\n")
	assert.Contains(t, out, "1.0T")
	assert.Contains(t, out, "no mounted filesystem")

	// With rates + capacity.
	m.rates["nvme0"] = ioRate{valid: true, readMBps: 100, writeMBps: 10, utilPct: 30}
	m.peakMBps["nvme0"] = 200
	d.Capacity = &monitor.Capacity{UsedBytes: 50 * 1 << 30, TotalBytes: 100 * 1 << 30, UsedKnown: true}
	out = strings.Join(m.renderActivity(d, 8, 20), "\n")
	assert.Contains(t, out, "100.0")
	assert.Contains(t, out, "MB/s")
	assert.Contains(t, out, "busy")
	assert.Contains(t, out, "50%")
	assert.Contains(t, out, "50.0G / 100G")
}

func TestRenderHelpContent(t *testing.T) {
	m := NewModel(nil, 0)
	m.width = 96
	m.showHelp = true
	out := m.render()
	for _, want := range []string{
		"What you're looking at", "Temp bars", "I/O bar", "busy %",
		"Disk", "Throttle", "Fans", "Keys", "toggle this help", "quit",
	} {
		assert.Contains(t, out, want)
	}
}

func TestHelpToggle(t *testing.T) {
	m := NewModel(nil, 0)

	// 'h' opens help.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	m = updated.(Model)
	assert.True(t, m.showHelp)

	// 'h' again closes it.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	m = updated.(Model)
	assert.False(t, m.showHelp)

	// 'esc' closes help when open.
	m.showHelp = true
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	assert.False(t, m.showHelp)
}

func TestQuitClosesHelpFirst(t *testing.T) {
	m := NewModel(nil, 0)
	m.showHelp = true

	// 'q' with help open should close help, not quit.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = updated.(Model)
	assert.False(t, m.showHelp)
	assert.Nil(t, cmd)

	// 'q' with help closed quits.
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.NotNil(t, cmd)
}

// modelWithDrives builds a Model whose snapshot holds n minimally-populated
// drives named nvme0..nvme(n-1), each with health data.
func modelWithDrives(n int) Model {
	m := NewModel(nil, 0)
	m.width = 100
	drives := make([]monitor.Drive, n)
	for i := range drives {
		drives[i] = monitor.Drive{
			Name:     fmt.Sprintf("nvme%d", i),
			Node:     fmt.Sprintf("/dev/nvme%d", i),
			Model:    "Test SSD",
			Throttle: &monitor.ThrottleStats{},
			Health: &monitor.SmartHealth{
				TempK: 310, AvailableSpare: 100, SpareThreshold: 5, PercentageUsed: 1,
				DataUnitsWritten: 1000, PowerOnHours: 100,
			},
		}
	}
	m.snap = &monitor.Snapshot{Drives: drives}
	return m
}

func keyPress(m Model, s string, code rune) Model {
	var msg tea.KeyPressMsg
	switch s {
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		msg = tea.KeyPressMsg{Code: code, Text: string(code)}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestTabCyclesSelection(t *testing.T) {
	m := modelWithDrives(3)
	assert.Equal(t, 0, m.selected)

	m = keyPress(m, "tab", 0)
	assert.Equal(t, 1, m.selected)
	m = keyPress(m, "tab", 0)
	assert.Equal(t, 2, m.selected)

	// Wraps back to the first drive.
	m = keyPress(m, "tab", 0)
	assert.Equal(t, 0, m.selected)

	// Shift+tab goes backward and wraps.
	m = keyPress(m, "shift+tab", 0)
	assert.Equal(t, 2, m.selected)
}

func TestTabNoopWithSingleDrive(t *testing.T) {
	m := modelWithDrives(1)
	m = keyPress(m, "tab", 0)
	assert.Equal(t, 0, m.selected)
}

func TestEnterOpensAndEscClosesDetail(t *testing.T) {
	m := modelWithDrives(2)
	m = keyPress(m, "tab", 0) // select nvme1
	require.Equal(t, 1, m.selected)

	m = keyPress(m, "enter", 0)
	assert.True(t, m.detail)

	out := m.render()
	assert.Contains(t, out, "nvme1")
	assert.Contains(t, out, "Wear & endurance")

	// esc backs out of the detail view first.
	m = keyPress(m, "esc", 0)
	assert.False(t, m.detail)
}

func TestEnterNoopWhenHelpOpen(t *testing.T) {
	m := modelWithDrives(2)
	m.showHelp = true
	m = keyPress(m, "enter", 0)
	assert.False(t, m.detail)
}

func TestSelectionClampsWhenDrivesShrink(t *testing.T) {
	m := modelWithDrives(3)
	m = keyPress(m, "tab", 0)
	m = keyPress(m, "tab", 0)
	require.Equal(t, 2, m.selected)
	m.detail = true

	// A new snapshot with a single drive must clamp selection and close detail
	// only when there are zero drives; with one drive it clamps to 0.
	updated, _ := m.Update(snapshotMsg{snap: &monitor.Snapshot{Drives: modelWithDrives(1).snap.Drives}})
	m = updated.(Model)
	assert.Equal(t, 0, m.selected)

	// Now drop to zero drives: detail must close.
	updated, _ = m.Update(snapshotMsg{snap: &monitor.Snapshot{}})
	m = updated.(Model)
	assert.Equal(t, 0, m.selected)
	assert.False(t, m.detail)
}

func TestRenderDetailHealthy(t *testing.T) {
	m := NewModel(nil, 0)
	m.width = 100
	d := monitor.Drive{
		Name: "nvme0", Node: "/dev/nvme0", Model: "Samsung SSD 990 PRO",
		Throttle: &monitor.ThrottleStats{Therm1TransCount: 7, Therm1TotalTime: 696},
		Health: &monitor.SmartHealth{
			TempK: 310, AvailableSpare: 100, SpareThreshold: 5, PercentageUsed: 2,
			DataUnitsRead: 74547585, DataUnitsWritten: 34180136,
			HostReadCommands: 548470530, HostWriteCmds: 579710735,
			ControllerBusyTime: 1062, PowerCycles: 682, PowerOnHours: 2000,
			UnsafeShutdowns: 13,
		},
	}
	out := m.renderDetail(d)
	for _, want := range []string{
		"nvme0", "/dev/nvme0", "healthy",
		"Wear & endurance", "Endurance used", "Spare available",
		"Lifetime activity", "Data written", "Power-on time",
		"Reliability", "Media errors",
		"Thermal", "Temperature", "37°C",
	} {
		assert.Contains(t, out, want, "detail view should mention %q", want)
	}
}

func TestRenderDetailFlagsProblems(t *testing.T) {
	m := NewModel(nil, 0)
	m.width = 100
	d := monitor.Drive{
		Name: "nvme0", Node: "/dev/nvme0",
		Throttle: &monitor.ThrottleStats{},
		Health: &monitor.SmartHealth{
			TempK: 320, AvailableSpare: 3, SpareThreshold: 10,
			PercentageUsed: 105, MediaErrors: 4,
		},
	}
	out := m.renderDetail(d)
	assert.Contains(t, out, "spare below threshold")
	assert.Contains(t, out, "endurance exhausted")
	assert.Contains(t, out, "media errors")
}

func TestRenderDetailUnavailable(t *testing.T) {
	m := NewModel(nil, 0)
	m.width = 100
	d := monitor.Drive{Name: "nvme0", Node: "/dev/nvme0", SmartErr: errors.New("permission denied")}
	out := m.renderDetail(d)
	assert.Contains(t, out, "unavailable")
	assert.Contains(t, out, "nvme-cli")
}

func TestHumanCount(t *testing.T) {
	assert.Equal(t, "0", humanCount(0))
	assert.Equal(t, "999", humanCount(999))
	assert.Equal(t, "1.0K", humanCount(1000))
	assert.Equal(t, "34.2M", humanCount(34180136))
	assert.Equal(t, "580M", humanCount(579710735))
}
