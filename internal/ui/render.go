package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rubiojr/nvmemon/internal/monitor"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff"))
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7a7a7a"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#c5c5c5"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ecc71")).Bold(true)
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#e67e22")).Bold(true)
	dangerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c")).Bold(true)
	driveStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5dade2"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3a3a3a")).
			Padding(0, 1)
)

// barWidth returns a sensible bar width for the current terminal size. It
// leaves room on the right for the per-row readouts (temperature limits,
// throughput figures, capacity totals).
func (m Model) barWidth() int {
	w := m.width - 52
	if w > 30 {
		w = 30
	}
	if w < 10 {
		w = 10
	}
	return w
}

func (m Model) render() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("nvmemon") + subtleStyle.Render("  ·  NVMe thermal monitor"))
	b.WriteByte('\n')

	status := "collecting…"
	if !m.updated.IsZero() {
		status = "updated " + m.updated.Format("15:04:05")
	}
	b.WriteString(subtleStyle.Render(fmt.Sprintf("%s  ·  refresh %s", status, m.interval)))
	b.WriteString("\n\n")

	if m.showHelp {
		b.WriteString(m.renderHelp())
		b.WriteByte('\n')
		b.WriteString(subtleStyle.Render("h/esc close · q quit"))
		return b.String()
	}

	if m.err != nil {
		b.WriteString(dangerStyle.Render("error: ") + m.err.Error() + "\n")
		b.WriteString(subtleStyle.Render("\nPress q to quit."))
		return b.String()
	}

	if m.snap == nil {
		b.WriteString(subtleStyle.Render("Reading sensors…"))
		return b.String()
	}

	if len(m.snap.Drives) == 0 {
		b.WriteString(warnStyle.Render("No NVMe drives found.") + "\n")
		b.WriteString(subtleStyle.Render("(Are you reading the right sysfs root? Try running where /sys is accessible.)\n"))
	}

	for _, d := range m.snap.Drives {
		b.WriteString(m.renderDrive(d))
		b.WriteByte('\n')
	}

	if len(m.snap.Fans) > 0 {
		b.WriteString(m.renderFans(m.snap.Fans))
		b.WriteByte('\n')
	}

	b.WriteString(subtleStyle.Render("h help · r refresh · q quit"))
	return b.String()
}

// renderHelp renders the help/legend screen explaining every row and metric.
func (m Model) renderHelp() string {
	heading := func(s string) string {
		return titleStyle.Render(s)
	}
	key := func(k, desc string) string {
		return "  " + driveStyle.Render(fmt.Sprintf("%-9s", k)) + subtleStyle.Render(desc)
	}
	item := func(label, desc string) string {
		return "  " + labelStyle.Render(fmt.Sprintf("%-12s", label)) + subtleStyle.Render(desc)
	}

	lines := []string{
		heading("What you're looking at"),
		"",
		item("Temp bars", "per-sensor temperature; gradient runs cool blue → green →"),
		item("", "yellow → orange → red. warn/crit are the drive's own limits."),
		item("I/O bar", "read+write throughput, scaled to the peak seen this session"),
		item("", "(relative, not a %). ↓ = read MB/s, ↑ = write MB/s."),
		item("busy %", "utilization: share of time the drive was doing I/O. A full"),
		item("", "I/O bar with low busy% just means bursty transfers with idle gaps."),
		item("Disk", "filesystem usage across all partitions on the drive (resolved"),
		item("", "through LVM/LUKS/btrfs). Total is the raw device capacity."),
		item("Throttle", "thermal throttling from SMART; needs root + nvme-cli."),
		item("Fans", "system fan speeds (RPM) for cross-reference."),
		"",
		heading("Keys"),
		"",
		key("h / ?", "toggle this help"),
		key("r", "refresh now"),
		key("q / esc", "quit"),
	}

	body := strings.Join(lines, "\n")
	return panelStyle.Width(min(m.width-2, 92)).Render(body)
}

func (m Model) renderDrive(d monitor.Drive) string {
	// Header line: name + model + pci + transport.
	header := driveStyle.Render(d.Name)
	meta := []string{}
	if d.Model != "" {
		meta = append(meta, d.Model)
	}
	if d.Address != "" {
		meta = append(meta, "PCI "+d.Address)
	}
	if d.Transport != "" {
		meta = append(meta, d.Transport)
	}
	if len(meta) > 0 {
		header += subtleStyle.Render("  ·  " + strings.Join(meta, "  ·  "))
	}

	rows := []string{header}

	// Sensor rows with gradient bars.
	labelW := 0
	for _, s := range d.Sensors {
		if len(s.Label) > labelW {
			labelW = len(s.Label)
		}
	}
	bw := m.barWidth()
	for _, s := range d.Sensors {
		label := labelStyle.Render(fmt.Sprintf("%-*s", labelW, s.Label))
		bar := renderBar(s.Celsius, bw)
		value := tempLabel(fmt.Sprintf("%5.1f°C", s.Celsius), s.Celsius)
		limits := ""
		if s.WarnC > 0 || s.CritC > 0 {
			limits = subtleStyle.Render(fmt.Sprintf("  warn %.0f crit %.0f", s.WarnC, s.CritC))
		}
		rows = append(rows, fmt.Sprintf("  %s  %s  %s%s", label, bar, value, limits))
	}

	// Activity rows: throughput / utilization and capacity.
	rows = append(rows, m.renderActivity(d, labelW, bw)...)

	// Cross-reference: throttling status.
	rows = append(rows, "  "+renderThrottle(d))

	body := strings.Join(rows, strings.Repeat("\n", rowGap+1))
	return panelStyle.Width(min(m.width-2, 92)).Render(body)
}

// rowGap is the number of blank lines inserted between rows inside a card.
const rowGap = 1

var (
	readStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5dd6c4")).Bold(true)
	writeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#c792ea")).Bold(true)
)

const throughputAccent = "#5dd6c4"

// renderActivity renders the throughput/utilization and capacity rows.
func (m Model) renderActivity(d monitor.Drive, labelW, bw int) []string {
	var rows []string

	// I/O row.
	ioLabel := labelStyle.Render(fmt.Sprintf("%-*s", labelW, "I/O"))
	r, ok := m.rates[d.Name]
	if !ok || !r.valid {
		rows = append(rows, fmt.Sprintf("  %s  %s", ioLabel,
			subtleStyle.Render("measuring…")))
	} else {
		peak := m.peakMBps[d.Name]
		if peak < 50 {
			peak = 50 // floor so an idle drive shows a small, stable bar
		}
		bar := renderAccentBar(r.total()/peak, bw, throughputAccent)
		stats := fmt.Sprintf("%s%s %s%s %s",
			readStyle.Render("↓"), readStyle.Render(fmt.Sprintf("%7.1f", r.readMBps)),
			writeStyle.Render("↑"), writeStyle.Render(fmt.Sprintf("%7.1f", r.writeMBps)),
			subtleStyle.Render("MB/s"))
		busy := lipgloss.NewStyle().Foreground(loadColorAt(r.utilPct / 100)).
			Render(fmt.Sprintf("busy %3.0f%%", r.utilPct))
		rows = append(rows, fmt.Sprintf("  %s  %s  %s  %s", ioLabel, bar, stats, busy))
	}

	// Disk capacity row.
	diskLabel := labelStyle.Render(fmt.Sprintf("%-*s", labelW, "Disk"))
	switch {
	case d.Capacity == nil || d.Capacity.TotalBytes == 0:
		rows = append(rows, fmt.Sprintf("  %s  %s", diskLabel,
			subtleStyle.Render("capacity unknown")))
	case !d.Capacity.UsedKnown:
		// We know the drive size but no filesystem on it is mounted.
		bar := renderPercentBar(0, bw)
		size := lipgloss.NewStyle().Foreground(labelStyle.GetForeground()).Bold(true).
			Render(humanBytes(d.Capacity.TotalBytes))
		rows = append(rows, fmt.Sprintf("  %s  %s  %s  %s", diskLabel, bar, size,
			subtleStyle.Render("no mounted filesystem")))
	default:
		frac := d.Capacity.UsedFraction()
		bar := renderPercentBar(frac*100, bw)
		pct := lipgloss.NewStyle().Foreground(loadColorAt(frac)).Bold(true).
			Render(fmt.Sprintf("%3.0f%%", frac*100))
		used := fmt.Sprintf("%s / %s",
			humanBytes(d.Capacity.UsedBytes), humanBytes(d.Capacity.TotalBytes))
		rows = append(rows, fmt.Sprintf("  %s  %s  %s  %s", diskLabel, bar, pct,
			subtleStyle.Render(used)))
	}

	return rows
}

// humanBytes formats a byte count using binary units (df -h style).
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	v := float64(n)
	exp := 0
	for v >= unit && exp < 5 {
		v /= unit
		exp++
	}
	suffix := []string{"K", "M", "G", "T", "P"}[exp-1]
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, suffix)
	}
	return fmt.Sprintf("%.1f%s", v, suffix)
}

func renderThrottle(d monitor.Drive) string {
	if d.Throttle == nil {
		reason := "throttle data unavailable"
		if d.SmartErr != nil {
			reason += " (needs root + nvme-cli)"
		}
		return subtleStyle.Render("⚠ " + reason)
	}
	t := *d.Throttle
	if !t.Throttled() {
		return okStyle.Render("✓ no thermal throttling on record")
	}
	parts := []string{}
	if t.Therm1TransCount > 0 {
		parts = append(parts, fmt.Sprintf("light×%d (%ds)", t.Therm1TransCount, t.Therm1TotalTime))
	}
	if t.Therm2TransCount > 0 {
		parts = append(parts, fmt.Sprintf("heavy×%d (%ds)", t.Therm2TransCount, t.Therm2TotalTime))
	}
	if t.WarningTempTime > 0 {
		parts = append(parts, fmt.Sprintf("%dmin >warn", t.WarningTempTime))
	}
	if t.CriticalCompTime > 0 {
		parts = append(parts, fmt.Sprintf("%dmin >crit", t.CriticalCompTime))
	}
	style := warnStyle
	if t.Therm2TransCount > 0 || t.CriticalCompTime > 0 {
		style = dangerStyle
	}
	return style.Render("⚠ throttled: " + strings.Join(parts, ", "))
}

func (m Model) renderFans(fans []monitor.Fan) string {
	var parts []string
	for _, f := range fans {
		name := f.Label
		if f.Chip != "" {
			name = f.Chip + "/" + f.Label
		}
		val := fmt.Sprintf("%d rpm", f.RPM)
		style := okStyle
		if f.RPM == 0 {
			style = subtleStyle
		}
		parts = append(parts, labelStyle.Render(name)+" "+style.Render(val))
	}
	body := titleStyle.Render("Fans") + "\n  " + strings.Join(parts, "   ")
	return panelStyle.Width(min(m.width-2, 92)).Render(body)
}
