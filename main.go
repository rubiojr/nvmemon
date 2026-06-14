// Command nvmemon is a terminal NVMe thermal monitor. It lists every attached
// NVMe drive with color-gradient temperature bars and cross-references each
// drive's thermal throttling counters and the system's fan speeds.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rubiojr/nvmemon/internal/monitor"
	"github.com/rubiojr/nvmemon/internal/ui"
	"github.com/rubiojr/nvmemon/internal/version"
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "nvmemon: unsupported OS %q; nvmemon only runs on Linux "+
			"(it reads sysfs and nvme-cli)\n", runtime.GOOS)
		os.Exit(1)
	}

	var (
		interval    = flag.Duration("interval", 2*time.Second, "refresh interval")
		sysfsRoot   = flag.String("sysfs", "/sys", "sysfs mount point")
		procMounts  = flag.String("proc-mounts", "/proc/mounts", "mount table path")
		noThrottle  = flag.Bool("no-throttle", false, "skip nvme-cli smart-log throttle collection")
		once        = flag.Bool("once", false, "print a single plain-text reading and exit (no TUI)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("nvmemon", version.String())
		return
	}

	c := monitor.New()
	c.SysfsRoot = *sysfsRoot
	c.ProcMounts = *procMounts
	if *noThrottle {
		c.Run = nil
	}

	if *once {
		if err := runOnce(c, *interval); err != nil {
			fmt.Fprintln(os.Stderr, "nvmemon:", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(ui.NewModel(c, *interval))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "nvmemon:", err)
		os.Exit(1)
	}
}

// runOnce collects two samples interval apart, derives rates, and prints a
// plain-text report. It's a non-interactive smoke test that also works over
// SSH or on headless boxes.
func runOnce(c *monitor.Collector, interval time.Duration) error {
	first, err := c.Collect()
	if err != nil {
		return err
	}
	t0 := time.Now()
	time.Sleep(interval)
	second, err := c.Collect()
	if err != nil {
		return err
	}
	dt := time.Since(t0).Seconds()

	prev := map[string]monitor.Drive{}
	for _, d := range first.Drives {
		prev[d.Name] = d
	}

	fmt.Println("nvmemon", version.String(), "-", time.Now().Format(time.RFC3339))
	for _, d := range second.Drives {
		fmt.Printf("\n%s  %s  PCI %s  %s\n", d.Name, d.Model, d.Address, d.Transport)
		for _, s := range d.Sensors {
			fmt.Printf("  %-10s %5.1f°C", s.Label, s.Celsius)
			if s.WarnC > 0 || s.CritC > 0 {
				fmt.Printf("  (warn %.0f crit %.0f)", s.WarnC, s.CritC)
			}
			fmt.Println()
		}
		printIO(d, prev[d.Name], dt)
		printCapacity(d)
		printThrottle(d)
		printHealth(d)
	}
	return nil
}

func printIO(cur, prev monitor.Drive, dt float64) {
	if cur.IO == nil || prev.IO == nil || dt <= 0 {
		fmt.Println("  I/O        (measuring…)")
		return
	}
	rd := mbps(cur.IO.ReadBytes(), prev.IO.ReadBytes(), dt)
	wr := mbps(cur.IO.WriteBytes(), prev.IO.WriteBytes(), dt)
	busy := 0.0
	if cur.IO.IoMillis >= prev.IO.IoMillis {
		busy = float64(cur.IO.IoMillis-prev.IO.IoMillis) / (dt * 1000) * 100
		if busy > 100 {
			busy = 100
		}
	}
	fmt.Printf("  I/O        read %.1f MB/s  write %.1f MB/s  busy %.0f%%\n", rd, wr, busy)
}

func printCapacity(d monitor.Drive) {
	if d.Capacity == nil || d.Capacity.TotalBytes == 0 {
		fmt.Println("  Disk       (capacity unknown)")
		return
	}
	total := humanGB(d.Capacity.TotalBytes)
	if !d.Capacity.UsedKnown {
		fmt.Printf("  Disk       %s  (no mounted filesystem)\n", total)
		return
	}
	fmt.Printf("  Disk       %.0f%%  %s / %s used\n",
		d.Capacity.UsedFraction()*100, humanGB(d.Capacity.UsedBytes), total)
}

func printThrottle(d monitor.Drive) {
	switch {
	case d.Throttle == nil:
		fmt.Println("  Throttle   (unavailable; needs root + nvme-cli)")
	case d.Throttle.Throttled():
		fmt.Printf("  Throttle   THROTTLED (light×%d heavy×%d warn %dmin)\n",
			d.Throttle.Therm1TransCount, d.Throttle.Therm2TransCount, d.Throttle.WarningTempTime)
	default:
		fmt.Println("  Throttle   none on record")
	}
}

func printHealth(d monitor.Drive) {
	h := d.Health
	if h == nil {
		fmt.Println("  Health     (unavailable; needs root + nvme-cli)")
		return
	}
	status := "OK"
	if h.CriticalWarning != 0 || h.MediaErrors > 0 || h.PercentageUsed >= 100 ||
		(h.SpareThreshold > 0 && h.AvailableSpare <= h.SpareThreshold) {
		status = "WARN"
	}
	fmt.Printf("  Health     %s  used %d%%  spare %d%%  written %s  on %dh  media-err %d\n",
		status, h.PercentageUsed, h.AvailableSpare,
		humanGB(h.BytesWritten()), h.PowerOnHours, h.MediaErrors)
}

func mbps(cur, prev uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return 0
	}
	return float64(cur-prev) / 1e6 / dt
}

func humanGB(n uint64) string {
	return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
}
