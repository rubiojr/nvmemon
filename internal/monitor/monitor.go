// Package monitor collects NVMe drive telemetry (temperatures, thermal
// thresholds, throttling counters) and system fan readings from the Linux
// sysfs hwmon interface and the nvme-cli tool.
//
// All filesystem access is rooted at a configurable path (SysfsRoot) and the
// nvme-cli invocation is injectable, which makes the collector fully testable
// against fixture data without requiring real hardware or root privileges.
package monitor

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// TempSensor is a single temperature channel exposed by a drive.
type TempSensor struct {
	Label   string  // e.g. "Composite", "Sensor 1"
	Celsius float64 // current temperature
	WarnC   float64 // manufacturer "high"/max threshold (0 if unknown)
	CritC   float64 // critical threshold (0 if unknown)
}

// ThrottleStats holds the thermal-management counters reported by the drive's
// SMART log. They are cumulative since the drive was manufactured.
type ThrottleStats struct {
	WarningTempTime  int // minutes spent above the warning threshold
	CriticalCompTime int // minutes spent above the critical threshold
	Therm1TransCount int // transitions into light throttle state
	Therm1TotalTime  int // seconds spent in light throttle state
	Therm2TransCount int // transitions into heavy throttle state
	Therm2TotalTime  int // seconds spent in heavy throttle state
}

// Throttled reports whether the drive has ever entered a thermal throttle
// state or spent time above its warning threshold.
func (t ThrottleStats) Throttled() bool {
	return t.Therm1TransCount > 0 || t.Therm2TransCount > 0 ||
		t.Therm1TotalTime > 0 || t.Therm2TotalTime > 0 ||
		t.WarningTempTime > 0 || t.CriticalCompTime > 0
}

// SmartHealth holds the wear, endurance and reliability counters reported by
// the drive's SMART log. These are the same figures `nvme smart-log` prints.
// Lifetime counters are cumulative since the drive was manufactured.
type SmartHealth struct {
	CriticalWarning int // critical warning bit field (0 = healthy)
	TempK           int // composite temperature, in Kelvin (SMART convention)
	AvailableSpare  int // remaining spare capacity, percent
	SpareThreshold  int // spare percent at which the drive warns
	PercentageUsed  int // estimated endurance consumed, percent (can exceed 100)

	DataUnitsRead    uint64 // 512KB units read over the drive's life
	DataUnitsWritten uint64 // 512KB units written over the drive's life
	HostReadCommands uint64 // host read commands issued
	HostWriteCmds    uint64 // host write commands issued

	ControllerBusyTime uint64 // minutes the controller was busy with I/O
	PowerCycles        uint64 // number of power cycles
	PowerOnHours       uint64 // cumulative power-on time, hours
	UnsafeShutdowns    uint64 // unexpected power loss count
	MediaErrors        uint64 // detected uncorrectable data integrity errors
	NumErrLogEntries   uint64 // entries in the error information log
}

// DataUnitBytes is the size in bytes of one SMART "data unit": 1000 * 512-byte
// logical blocks (512000 bytes), per the NVMe specification.
const DataUnitBytes = 512 * 1000

// BytesRead returns total host-read bytes derived from the data-unit counter.
func (h SmartHealth) BytesRead() uint64 { return h.DataUnitsRead * DataUnitBytes }

// BytesWritten returns total host-written bytes derived from the data-unit counter.
func (h SmartHealth) BytesWritten() uint64 { return h.DataUnitsWritten * DataUnitBytes }

// TempC returns the composite temperature in Celsius, or false when the drive
// did not report one.
func (h SmartHealth) TempC() (float64, bool) {
	if h.TempK == 0 {
		return 0, false
	}
	return float64(h.TempK) - 273.15, true
}

// Drive is a single NVMe device with its sensors and (optional) throttle data.
type Drive struct {
	Name      string // controller name, e.g. "nvme0"
	Node      string // device node, e.g. "/dev/nvme0"
	Model     string
	Address   string // PCI address, e.g. "0000:03:00.0"
	Transport string // e.g. "pcie"
	Sensors   []TempSensor
	Throttle  *ThrottleStats // nil when smart-log was unavailable
	Health    *SmartHealth   // nil when smart-log was unavailable
	SmartErr  error          // why throttle data is missing, if any

	// IO holds cumulative block-I/O counters summed across the drive's
	// namespaces. Rates are derived by diffing two snapshots over time.
	// nil when no namespace stats could be read.
	IO *IOCounters
	// Capacity holds aggregate filesystem usage across mounted partitions of
	// this drive. nil when nothing backed by the drive is mounted.
	Capacity *Capacity
}

// Composite returns the primary ("Composite") sensor when present, otherwise
// the hottest sensor. The bool is false when the drive has no sensors.
func (d Drive) Composite() (TempSensor, bool) {
	if len(d.Sensors) == 0 {
		return TempSensor{}, false
	}
	best := d.Sensors[0]
	for _, s := range d.Sensors {
		if strings.EqualFold(s.Label, "Composite") {
			return s, true
		}
		if s.Celsius > best.Celsius {
			best = s
		}
	}
	return best, true
}

// Fan is a single system fan reading.
type Fan struct {
	Label string // e.g. "fan1" or a chip-provided label
	Chip  string // hwmon chip name, e.g. "thinkpad"
	RPM   int
}

// Snapshot is a single point-in-time collection of all telemetry.
type Snapshot struct {
	Drives []Drive
	Fans   []Fan
}

// Runner executes an external command and returns its stdout. It is injectable
// for testing. A nil Runner disables throttle collection.
type Runner func(name string, args ...string) ([]byte, error)

// execRunner is the default Runner backed by os/exec.
func execRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// Collector gathers telemetry. The zero value is not usable; use New.
type Collector struct {
	// SysfsRoot is the mount point of sysfs (normally "/sys"). hwmon devices
	// are read from <SysfsRoot>/class/hwmon.
	SysfsRoot string
	// Run executes nvme-cli. When nil, throttle data is skipped.
	Run Runner
	// ProcMounts is the path to the mount table (normally "/proc/mounts").
	// When empty, filesystem capacity collection is skipped.
	ProcMounts string
	// Statfs reports total and used bytes for a mountpoint. When nil,
	// filesystem capacity collection is skipped.
	Statfs StatfsFunc
}

// New returns a Collector reading from the real /sys and the real nvme binary.
func New() *Collector {
	return &Collector{
		SysfsRoot:  "/sys",
		Run:        execRunner,
		ProcMounts: "/proc/mounts",
		Statfs:     unixStatfs,
	}
}

// Collect reads a fresh Snapshot.
func (c *Collector) Collect() (*Snapshot, error) {
	hwmonDir := filepath.Join(c.SysfsRoot, "class", "hwmon")
	entries, err := os.ReadDir(hwmonDir)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{}
	for _, e := range entries {
		dir := filepath.Join(hwmonDir, e.Name())
		name := readStr(filepath.Join(dir, "name"))
		switch name {
		case "nvme":
			if d, ok := c.readDrive(dir); ok {
				snap.Drives = append(snap.Drives, d)
			}
		default:
			snap.Fans = append(snap.Fans, readFans(dir, name)...)
		}
	}

	sort.Slice(snap.Drives, func(i, j int) bool {
		return snap.Drives[i].Name < snap.Drives[j].Name
	})
	sort.Slice(snap.Fans, func(i, j int) bool {
		if snap.Fans[i].Chip != snap.Fans[j].Chip {
			return snap.Fans[i].Chip < snap.Fans[j].Chip
		}
		return snap.Fans[i].Label < snap.Fans[j].Label
	})
	return snap, nil
}

// readDrive builds a Drive from an nvme hwmon directory.
func (c *Collector) readDrive(hwmonDir string) (Drive, bool) {
	devLink := filepath.Join(hwmonDir, "device")
	devPath, err := filepath.EvalSymlinks(devLink)
	if err != nil {
		// Some fixtures use a real subdir rather than a symlink.
		devPath = devLink
	}

	d := Drive{
		Name:      filepath.Base(devPath),
		Model:     strings.TrimSpace(readStr(filepath.Join(devPath, "model"))),
		Address:   strings.TrimSpace(readStr(filepath.Join(devPath, "address"))),
		Transport: strings.TrimSpace(readStr(filepath.Join(devPath, "transport"))),
	}
	if d.Name == "" || d.Name == "device" || d.Name == "." {
		return Drive{}, false
	}
	d.Node = "/dev/" + d.Name
	d.Sensors = readSensors(hwmonDir)
	c.attachBlockIO(&d, devPath)

	if c.Run != nil {
		if ts, health, err := c.smartLog(d.Node); err != nil {
			d.SmartErr = err
		} else {
			d.Throttle = ts
			d.Health = health
		}
	} else {
		d.SmartErr = errors.New("smart-log collection disabled")
	}
	return d, true
}

// readSensors reads all tempN_* channels from a hwmon directory.
func readSensors(dir string) []TempSensor {
	inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
	sort.Strings(inputs)

	var sensors []TempSensor
	for _, in := range inputs {
		prefix := strings.TrimSuffix(in, "_input") // <dir>/tempN
		raw, ok := readInt(in)
		if !ok {
			continue
		}
		label := strings.TrimSpace(readStr(prefix + "_label"))
		if label == "" {
			label = filepath.Base(prefix) // fall back to "tempN"
		}
		s := TempSensor{Label: label, Celsius: milliToC(raw)}
		if v, ok := readInt(prefix + "_max"); ok {
			s.WarnC = milliToC(v)
		}
		if v, ok := readInt(prefix + "_crit"); ok {
			s.CritC = milliToC(v)
		}
		sensors = append(sensors, s)
	}
	return sensors
}

// readFans reads all fanN_input channels from a hwmon directory.
func readFans(dir, chip string) []Fan {
	inputs, _ := filepath.Glob(filepath.Join(dir, "fan*_input"))
	sort.Strings(inputs)

	var fans []Fan
	for _, in := range inputs {
		rpm, ok := readInt(in)
		if !ok {
			continue
		}
		prefix := strings.TrimSuffix(in, "_input")
		label := strings.TrimSpace(readStr(prefix + "_label"))
		if label == "" {
			label = filepath.Base(prefix)
		}
		fans = append(fans, Fan{Label: label, Chip: chip, RPM: rpm})
	}
	return fans
}

// smartLogJSON mirrors the subset of `nvme smart-log -o json` we care about.
// Large counters are emitted by nvme-cli as JSON numbers; uint64 is sufficient
// for any realistic drive lifetime.
type smartLogJSON struct {
	WarningTempTime  int `json:"warning_temp_time"`
	CriticalCompTime int `json:"critical_comp_time"`
	Therm1TransCount int `json:"thm_temp1_trans_count"`
	Therm1TotalTime  int `json:"thm_temp1_total_time"`
	Therm2TransCount int `json:"thm_temp2_trans_count"`
	Therm2TotalTime  int `json:"thm_temp2_total_time"`

	CriticalWarning int `json:"critical_warning"`
	Temperature     int `json:"temperature"`
	AvailSpare      int `json:"avail_spare"`
	SpareThresh     int `json:"spare_thresh"`
	PercentUsed     int `json:"percent_used"`

	DataUnitsRead     uint64 `json:"data_units_read"`
	DataUnitsWritten  uint64 `json:"data_units_written"`
	HostReadCommands  uint64 `json:"host_read_commands"`
	HostWriteCommands uint64 `json:"host_write_commands"`

	ControllerBusyTime uint64 `json:"controller_busy_time"`
	PowerCycles        uint64 `json:"power_cycles"`
	PowerOnHours       uint64 `json:"power_on_hours"`
	UnsafeShutdowns    uint64 `json:"unsafe_shutdowns"`
	MediaErrors        uint64 `json:"media_errors"`
	NumErrLogEntries   uint64 `json:"num_err_log_entries"`
}

// smartLog invokes nvme-cli and parses the thermal counters and health stats.
func (c *Collector) smartLog(node string) (*ThrottleStats, *SmartHealth, error) {
	out, err := c.Run("nvme", "smart-log", node, "-o", "json")
	if err != nil {
		return nil, nil, err
	}
	var raw smartLogJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, nil, err
	}
	ts := &ThrottleStats{
		WarningTempTime:  raw.WarningTempTime,
		CriticalCompTime: raw.CriticalCompTime,
		Therm1TransCount: raw.Therm1TransCount,
		Therm1TotalTime:  raw.Therm1TotalTime,
		Therm2TransCount: raw.Therm2TransCount,
		Therm2TotalTime:  raw.Therm2TotalTime,
	}
	health := &SmartHealth{
		CriticalWarning:    raw.CriticalWarning,
		TempK:              raw.Temperature,
		AvailableSpare:     raw.AvailSpare,
		SpareThreshold:     raw.SpareThresh,
		PercentageUsed:     raw.PercentUsed,
		DataUnitsRead:      raw.DataUnitsRead,
		DataUnitsWritten:   raw.DataUnitsWritten,
		HostReadCommands:   raw.HostReadCommands,
		HostWriteCmds:      raw.HostWriteCommands,
		ControllerBusyTime: raw.ControllerBusyTime,
		PowerCycles:        raw.PowerCycles,
		PowerOnHours:       raw.PowerOnHours,
		UnsafeShutdowns:    raw.UnsafeShutdowns,
		MediaErrors:        raw.MediaErrors,
		NumErrLogEntries:   raw.NumErrLogEntries,
	}
	return ts, health, nil
}

// --- small sysfs helpers ---

func readStr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

func readInt(path string) (int, bool) {
	s := strings.TrimSpace(readStr(path))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseUint parses an unsigned base-10 integer, returning false on failure.
func parseUint(s string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// milliToC converts millidegrees Celsius (sysfs convention) to Celsius.
func milliToC(milli int) float64 {
	return float64(milli) / 1000.0
}
