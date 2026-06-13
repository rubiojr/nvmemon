// Command nvmemon is a terminal NVMe thermal monitor. It lists every attached
// NVMe drive with color-gradient temperature bars and cross-references each
// drive's thermal throttling counters and the system's fan speeds.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rubiojr/nvmemon/internal/monitor"
	"github.com/rubiojr/nvmemon/internal/ui"
)

func main() {
	var (
		interval   = flag.Duration("interval", 2*time.Second, "refresh interval")
		sysfsRoot  = flag.String("sysfs", "/sys", "sysfs mount point")
		noThrottle = flag.Bool("no-throttle", false, "skip nvme-cli smart-log throttle collection")
	)
	flag.Parse()

	c := monitor.New()
	c.SysfsRoot = *sysfsRoot
	if *noThrottle {
		c.Run = nil
	}

	p := tea.NewProgram(ui.NewModel(c, *interval))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "nvmemon:", err)
		os.Exit(1)
	}
}
