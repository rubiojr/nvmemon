package ui

import (
	"fmt"
	"os"
	"testing"

	"github.com/rubiojr/nvmemon/internal/monitor"
)

// TestPreview is a manual visual check. Run it with:
//
//	NVMEMON_PREVIEW=1 go test ./internal/ui -run TestPreview -v
//
// to print a fully rendered frame with real ANSI colors.
func TestPreview(t *testing.T) {
	if os.Getenv("NVMEMON_PREVIEW") == "" {
		t.Skip("set NVMEMON_PREVIEW=1 to render the visual preview")
	}
	m := NewModel(nil, 2_000_000_000)
	m.width = 96
	m.rates = map[string]ioRate{
		"nvme0": {valid: true, readMBps: 1234.5, writeMBps: 56.7, utilPct: 78},
		"nvme1": {valid: true, readMBps: 12.3, writeMBps: 0, utilPct: 4},
	}
	m.peakMBps = map[string]float64{"nvme0": 1600, "nvme1": 500}
	m.snap = &monitor.Snapshot{
		Drives: []monitor.Drive{
			{
				Name: "nvme0", Model: "Internal SSD 1TB", Address: "0000:c1:00.0", Transport: "pcie",
				Sensors: []monitor.TempSensor{
					{Label: "Composite", Celsius: 56.9, WarnC: 85.8, CritC: 87.8},
					{Label: "Sensor 1", Celsius: 56.9},
				},
				Throttle: &monitor.ThrottleStats{Therm1TransCount: 2, Therm1TotalTime: 30, WarningTempTime: 4},
				Capacity: &monitor.Capacity{UsedBytes: 472 * 1 << 30, TotalBytes: 1000 * 1 << 30},
			},
			{
				Name: "nvme1", Model: "USB4 Enclosure 2TB", Address: "0000:03:00.0", Transport: "pcie",
				Sensors: []monitor.TempSensor{
					{Label: "Composite", Celsius: 32.9, WarnC: 81.8, CritC: 84.8},
					{Label: "Sensor 1", Celsius: 32.9},
					{Label: "Sensor 2", Celsius: 34.9},
				},
				Throttle: &monitor.ThrottleStats{},
				Capacity: &monitor.Capacity{UsedBytes: 1500 * 1 << 30, TotalBytes: 2000 * 1 << 30},
			},
		},
		Fans: []monitor.Fan{
			{Label: "fan1", Chip: "thinkpad", RPM: 3592},
			{Label: "fan2", Chip: "thinkpad", RPM: 3592},
		},
	}
	fmt.Println("\n" + m.render())
}
