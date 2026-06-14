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

	// selectedPanelStyle highlights the currently focused drive card.
	selectedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#5dade2")).
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

	// Detailed SMART view for the selected drive.
	if m.detail {
		if d, ok := m.selectedDrive(); ok {
			b.WriteString(m.renderDetail(d))
			b.WriteByte('\n')
			b.WriteString(subtleStyle.Render("tab/↑↓ switch drive · enter/esc back · q quit"))
			return b.String()
		}
		// No drive to detail; fall through to the list.
	}

	if len(m.snap.Drives) == 0 {
		b.WriteString(warnStyle.Render("No NVMe drives found.") + "\n")
		b.WriteString(subtleStyle.Render("(Are you reading the right sysfs root? Try running where /sys is accessible.)\n"))
	}

	multi := len(m.snap.Drives) > 1
	for i, d := range m.snap.Drives {
		b.WriteString(m.renderDrive(d, multi && i == m.selected))
		b.WriteByte('\n')
	}

	if len(m.snap.Fans) > 0 {
		b.WriteString(m.renderFans(m.snap.Fans))
		b.WriteByte('\n')
	}

	hint := "h help · r refresh · q quit"
	if multi {
		hint = "tab switch · enter details · h help · r refresh · q quit"
	} else if len(m.snap.Drives) == 1 {
		hint = "enter details · h help · r refresh · q quit"
	}
	b.WriteString(subtleStyle.Render(hint))
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
		item("", "light×N / heavy×N are TMT1/TMT2 transition counts: how many"),
		item("", "times the drive throttled to cool itself (light = mild, heavy ="),
		item("", "aggressive), with seconds spent in each. >warn/>crit are minutes"),
		item("", "above the drive's thresholds. all are lifetime totals; 0 is ideal."),
		item("Fans", "system fan speeds (RPM) for cross-reference."),
		"",
		heading("Keys"),
		"",
		key("tab", "select next drive (shift+tab / ↑↓ also move)"),
		key("enter", "open the selected drive's full SMART health"),
		key("h / ?", "toggle this help"),
		key("r", "refresh now"),
		key("q / esc", "back out · quit"),
	}

	body := strings.Join(lines, "\n")
	return panelStyle.Width(min(m.width-2, 92)).Render(body)
}

// detailLabelW is the column width for field labels in the detail view.
const detailLabelW = 20

// renderDetail renders the full SMART health screen for a single drive.
func (m Model) renderDetail(d monitor.Drive) string {
	// Header: identity.
	header := driveStyle.Render(d.Name) + subtleStyle.Render("  ("+d.Node+")")
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

	rows := []string{header}
	if len(meta) > 0 {
		rows = append(rows, subtleStyle.Render("  "+strings.Join(meta, "  ·  ")))
	}
	rows = append(rows, "")

	if d.Health == nil {
		reason := "SMART health unavailable"
		if d.SmartErr != nil {
			reason += " — needs root + nvme-cli"
		}
		rows = append(rows, warnStyle.Render("⚠ "+reason))
		body := strings.Join(rows, "\n")
		return panelStyle.Width(min(m.width-2, 92)).Render(body)
	}

	rows = append(rows, m.detailHealthRows(d, *d.Health)...)

	body := strings.Join(rows, "\n")
	return panelStyle.Width(min(m.width-2, 92)).Render(body)
}

// detailField formats one "label  value" row for the detail view.
func detailField(label, value string) string {
	return "  " + labelStyle.Render(fmt.Sprintf("%-*s", detailLabelW, label)) + value
}

// detailSection renders a bold section heading for the detail view.
func detailSection(s string) string {
	return titleStyle.Render(s)
}

// detailHealthRows builds the grouped SMART tables for a drive's health log.
func (m Model) detailHealthRows(d monitor.Drive, h monitor.SmartHealth) []string {
	var rows []string

	// Overall verdict.
	rows = append(rows, detailField("Health", healthVerdict(h)))
	if h.PercentageUsed != 0 || h.AvailableSpare != 0 || h.SpareThreshold != 0 {
		rows = append(rows, "")
	}

	// Wear & endurance.
	rows = append(rows, detailSection("Wear & endurance"))
	bw := m.detailBarWidth()
	{
		// percentage_used: 0..100+ where 100% = rated endurance reached.
		usedPct := float64(h.PercentageUsed)
		bar := renderPercentBar(usedPct, bw)
		val := pctColor(usedPct/100, fmt.Sprintf("%d%%", h.PercentageUsed))
		rows = append(rows, detailField("Endurance used", fmt.Sprintf("%s  %s", bar, val)))

		// available_spare: 100..0 where dropping below threshold is bad.
		spare := float64(h.AvailableSpare)
		spareBar := renderPercentBar(spare, bw)
		spareVal := fmt.Sprintf("%d%%", h.AvailableSpare)
		if h.SpareThreshold > 0 && h.AvailableSpare <= h.SpareThreshold {
			spareVal = dangerStyle.Render(spareVal + " ⚠ below threshold")
		} else {
			spareVal = okStyle.Render(spareVal)
		}
		rows = append(rows, detailField("Spare available", fmt.Sprintf("%s  %s", spareBar, spareVal)))
		rows = append(rows, detailField("Spare threshold", subtleStyle.Render(fmt.Sprintf("%d%%", h.SpareThreshold))))
	}
	rows = append(rows, "")

	// Lifetime activity.
	rows = append(rows, detailSection("Lifetime activity"))
	rows = append(rows, detailField("Data written", fmt.Sprintf("%s  %s",
		humanBytes(h.BytesWritten()), subtleStyle.Render(fmt.Sprintf("(%s units)", humanCount(h.DataUnitsWritten))))))
	rows = append(rows, detailField("Data read", fmt.Sprintf("%s  %s",
		humanBytes(h.BytesRead()), subtleStyle.Render(fmt.Sprintf("(%s units)", humanCount(h.DataUnitsRead))))))
	rows = append(rows, detailField("Host writes", subtleStyle.Render(humanCount(h.HostWriteCmds)+" commands")))
	rows = append(rows, detailField("Host reads", subtleStyle.Render(humanCount(h.HostReadCommands)+" commands")))
	rows = append(rows, detailField("Power-on time", humanHours(h.PowerOnHours)))
	rows = append(rows, detailField("Power cycles", fmt.Sprintf("%d", h.PowerCycles)))
	rows = append(rows, detailField("Controller busy", humanMinutes(h.ControllerBusyTime)))
	rows = append(rows, "")

	// Reliability.
	rows = append(rows, detailSection("Reliability"))
	rows = append(rows, detailField("Critical warning", warnIfNonzero(uint64(h.CriticalWarning), fmt.Sprintf("0x%02x", h.CriticalWarning))))
	rows = append(rows, detailField("Media errors", warnIfNonzero(h.MediaErrors, fmt.Sprintf("%d", h.MediaErrors))))
	rows = append(rows, detailField("Error log entries", warnIfNonzero(h.NumErrLogEntries, fmt.Sprintf("%d", h.NumErrLogEntries))))
	rows = append(rows, detailField("Unsafe shutdowns", subtleStyle.Render(fmt.Sprintf("%d", h.UnsafeShutdowns))))
	rows = append(rows, "")

	// Thermal.
	rows = append(rows, detailSection("Thermal"))
	if c, ok := h.TempC(); ok {
		rows = append(rows, detailField("Temperature", tempLabel(fmt.Sprintf("%.0f°C", c), c)))
	}
	rows = append(rows, detailField("Throttling", renderThrottle(d)))

	return rows
}

// detailBarWidth returns a compact bar width for the detail view.
func (m Model) detailBarWidth() int {
	w := m.width - 40
	if w > 24 {
		w = 24
	}
	if w < 8 {
		w = 8
	}
	return w
}

// healthVerdict summarizes a drive's SMART status into a single colored phrase.
func healthVerdict(h monitor.SmartHealth) string {
	var problems []string
	if h.CriticalWarning != 0 {
		problems = append(problems, "critical warning set")
	}
	if h.SpareThreshold > 0 && h.AvailableSpare <= h.SpareThreshold {
		problems = append(problems, "spare below threshold")
	}
	if h.PercentageUsed >= 100 {
		problems = append(problems, "endurance exhausted")
	}
	if h.MediaErrors > 0 {
		problems = append(problems, "media errors")
	}
	if len(problems) == 0 {
		return okStyle.Render("✓ healthy")
	}
	return dangerStyle.Render("⚠ " + strings.Join(problems, ", "))
}

// pctColor colors text by its position on the load gradient (green->red).
func pctColor(frac float64, text string) string {
	return lipgloss.NewStyle().Foreground(loadColorAt(clamp01(frac))).Bold(true).Render(text)
}

// warnIfNonzero renders text in red when n > 0, otherwise in green.
func warnIfNonzero(n uint64, text string) string {
	if n > 0 {
		return dangerStyle.Render(text)
	}
	return okStyle.Render(text)
}

func (m Model) renderDrive(d monitor.Drive, selected bool) string {
	// Header line: name + model + pci + transport.
	name := d.Name
	if selected {
		name = "▸ " + name
	}
	header := driveStyle.Render(name)
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
	style := panelStyle
	if selected {
		style = selectedPanelStyle
	}
	return style.Width(min(m.width-2, 92)).Render(body)
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

// humanCount formats a large count with K/M/B/T suffixes (decimal).
func humanCount(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d", n)
	}
	v := float64(n)
	exp := 0
	for v >= unit && exp < 4 {
		v /= unit
		exp++
	}
	suffix := []string{"K", "M", "B", "T"}[exp-1]
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, suffix)
	}
	return fmt.Sprintf("%.1f%s", v, suffix)
}

// humanHours renders a power-on hour count as "N h" plus an approximate
// years/days breakdown once it is large enough to be meaningful.
func humanHours(h uint64) string {
	base := fmt.Sprintf("%d h", h)
	if h < 24 {
		return base
	}
	days := h / 24
	if days < 365 {
		return fmt.Sprintf("%s  (~%d days)", base, days)
	}
	years := float64(h) / 24 / 365
	return fmt.Sprintf("%s  (~%.1f years)", base, years)
}

// humanMinutes renders a minute count as "N min" with an hours hint when large.
func humanMinutes(min uint64) string {
	if min < 60 {
		return fmt.Sprintf("%d min", min)
	}
	return fmt.Sprintf("%d min  (~%d h)", min, min/60)
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
