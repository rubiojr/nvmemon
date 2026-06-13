package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rubiojr/nvmemon/internal/monitor"
)

// snapshotMsg carries the result of a collection cycle.
type snapshotMsg struct {
	snap *monitor.Snapshot
	err  error
}

// tickMsg signals it's time to collect again.
type tickMsg time.Time

// ioRate holds derived per-drive activity computed by diffing two snapshots.
type ioRate struct {
	valid     bool
	readMBps  float64
	writeMBps float64
	utilPct   float64
}

// total returns combined read+write throughput in MB/s.
func (r ioRate) total() float64 { return r.readMBps + r.writeMBps }

// Model is the Bubble Tea model for nvmemon.
type Model struct {
	collector *monitor.Collector
	interval  time.Duration

	snap    *monitor.Snapshot
	err     error
	updated time.Time

	rates    map[string]ioRate  // drive name -> derived rate
	peakMBps map[string]float64 // drive name -> peak throughput, for bar scaling

	width  int
	height int
}

// NewModel builds a Model that polls c every interval.
func NewModel(c *monitor.Collector, interval time.Duration) Model {
	return Model{
		collector: c,
		interval:  interval,
		width:     80,
		rates:     map[string]ioRate{},
		peakMBps:  map[string]float64{},
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.collect(), tea.Tick(m.interval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	}))
}

// collect runs one collection cycle off the UI goroutine.
func (m Model) collect() tea.Cmd {
	return func() tea.Msg {
		snap, err := m.collector.Collect()
		return snapshotMsg{snap: snap, err: err}
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, m.collect()
		}
		return m, nil

	case snapshotMsg:
		now := time.Now()
		m.computeRates(msg.snap, now)
		m.snap = msg.snap
		m.err = msg.err
		m.updated = now
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.collect(), tea.Tick(m.interval, func(t time.Time) tea.Msg {
			return tickMsg(t)
		}))
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// computeRates derives per-drive throughput and utilization by diffing the
// incoming snapshot against the previous one. Results are stored in m.rates.
func (m *Model) computeRates(next *monitor.Snapshot, now time.Time) {
	if next == nil {
		return
	}
	if m.peakMBps == nil {
		m.peakMBps = map[string]float64{}
	}

	dt := now.Sub(m.updated).Seconds()
	prev := previousDrives(m.snap)

	fresh := make(map[string]ioRate, len(next.Drives))
	for _, d := range next.Drives {
		r := driveRate(d, prev[d.Name], dt)
		fresh[d.Name] = r
		if t := r.total(); r.valid && t > m.peakMBps[d.Name] {
			m.peakMBps[d.Name] = t
		}
	}
	m.rates = fresh
}

// previousDrives indexes a snapshot's drives by name.
func previousDrives(snap *monitor.Snapshot) map[string]monitor.Drive {
	out := map[string]monitor.Drive{}
	if snap != nil {
		for _, d := range snap.Drives {
			out[d.Name] = d
		}
	}
	return out
}

// driveRate computes one drive's rate from the current and previous samples.
// It returns an invalid (zero) rate when there isn't enough history.
func driveRate(cur, prev monitor.Drive, dt float64) ioRate {
	if cur.IO == nil || prev.IO == nil || dt <= 0 {
		return ioRate{}
	}
	r := ioRate{valid: true}
	r.readMBps = deltaMBps(cur.IO.ReadBytes(), prev.IO.ReadBytes(), dt)
	r.writeMBps = deltaMBps(cur.IO.WriteBytes(), prev.IO.WriteBytes(), dt)
	if cur.IO.IoMillis >= prev.IO.IoMillis {
		busyMs := float64(cur.IO.IoMillis - prev.IO.IoMillis)
		r.utilPct = clampPct(busyMs / (dt * 1000) * 100)
	}
	return r
}

// deltaMBps converts a byte delta over dt seconds into MB/s (decimal MB).
func deltaMBps(cur, prev uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return 0
	}
	return float64(cur-prev) / 1e6 / dt
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
