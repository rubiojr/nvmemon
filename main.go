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
		noThrottle  = flag.Bool("no-throttle", false, "skip nvme-cli smart-log throttle collection")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("nvmemon", version.String())
		return
	}

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
