package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSysfs builds a minimal sysfs tree under a temp dir and returns its root.
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Backing nvme controller device directories.
	mkDevice(t, root, "nvme0", map[string]string{
		"model":     "Internal SSD 1TB",
		"address":   "0000:c1:00.0",
		"transport": "pcie",
	})
	mkDevice(t, root, "nvme1", map[string]string{
		"model":     "USB4 Enclosure 2TB",
		"address":   "0000:03:00.0",
		"transport": "pcie",
	})

	// hwmon0 -> nvme0, one composite sensor, hot.
	mkNvmeHwmon(t, root, "hwmon0", "nvme0", []sensorFix{
		{"temp1", "Composite", 56900, 85800, 87800},
		{"temp2", "Sensor 1", 56900, 0, 0},
	})
	// hwmon1 -> nvme1, two sensors, cool.
	mkNvmeHwmon(t, root, "hwmon1", "nvme1", []sensorFix{
		{"temp1", "Composite", 32900, 81800, 84800},
		{"temp2", "Sensor 1", 32900, 0, 0},
		{"temp3", "Sensor 2", 34900, 0, 0},
	})
	// hwmon2 -> thinkpad fans.
	mkFanHwmon(t, root, "hwmon2", "thinkpad", map[string]int{
		"fan1": 3592,
		"fan2": 3592,
	})

	// Namespaces with block stats (read=2000 sectors, write=1000, io_ticks=500).
	mkNamespace(t, root, "nvme0", "nvme0n1", 2000, 1000, 500)
	mkNamespace(t, root, "nvme1", "nvme1n1", 10, 0, 8)

	return root
}

// mkNamespace creates a namespace block dir with a "stat" file under the
// controller's device directory.
func mkNamespace(t *testing.T, root, ctrl, ns string, readSectors, writeSectors, ioMs int) {
	t.Helper()
	dir := filepath.Join(root, "devices", ctrl, ns)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	// Block stat: 11 fields; [2]=read sectors, [6]=write sectors, [9]=io_ticks.
	fields := []int{0, 0, readSectors, 0, 0, 0, writeSectors, 0, 0, ioMs, 0}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = strconv.Itoa(f)
	}
	writeFile(t, filepath.Join(dir, "stat"), strings.Join(parts, " "))
}

type sensorFix struct {
	name             string
	label            string
	input, max, crit int
}

func mkDevice(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "devices", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for k, v := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, k), []byte(v+"\n"), 0o644))
	}
}

func mkNvmeHwmon(t *testing.T, root, hwmon, device string, sensors []sensorFix) {
	t.Helper()
	dir := filepath.Join(root, "class", "hwmon", hwmon)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writeFile(t, filepath.Join(dir, "name"), "nvme")
	// device symlink -> backing device dir.
	target := filepath.Join(root, "devices", device)
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "device")))
	for _, s := range sensors {
		writeFile(t, filepath.Join(dir, s.name+"_input"), itoa(s.input))
		writeFile(t, filepath.Join(dir, s.name+"_label"), s.label)
		if s.max != 0 {
			writeFile(t, filepath.Join(dir, s.name+"_max"), itoa(s.max))
		}
		if s.crit != 0 {
			writeFile(t, filepath.Join(dir, s.name+"_crit"), itoa(s.crit))
		}
	}
}

func mkFanHwmon(t *testing.T, root, hwmon, name string, fans map[string]int) {
	t.Helper()
	dir := filepath.Join(root, "class", "hwmon", hwmon)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writeFile(t, filepath.Join(dir, "name"), name)
	for fan, rpm := range fans {
		writeFile(t, filepath.Join(dir, fan+"_input"), itoa(rpm))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content+"\n"), 0o644))
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func TestCollect(t *testing.T) {
	root := fakeSysfs(t)

	// Fake nvme runner: nvme0 throttled, nvme1 clean, others error.
	runner := func(name string, args ...string) ([]byte, error) {
		require.Equal(t, "nvme", name)
		// args: smart-log <node> -o json
		require.Len(t, args, 4)
		node := args[1]
		switch node {
		case "/dev/nvme0":
			return []byte(`{"warning_temp_time":7,"critical_comp_time":0,"thm_temp1_trans_count":3,"thm_temp1_total_time":42,"thm_temp2_trans_count":1,"thm_temp2_total_time":5}`), nil
		case "/dev/nvme1":
			return []byte(`{"warning_temp_time":0,"thm_temp1_trans_count":0,"thm_temp2_trans_count":0}`), nil
		default:
			return nil, errors.New("unexpected node")
		}
	}

	c := &Collector{SysfsRoot: root, Run: runner}
	snap, err := c.Collect()
	require.NoError(t, err)
	require.Len(t, snap.Drives, 2)

	// Sorted by name: nvme0 first.
	d0 := snap.Drives[0]
	assert.Equal(t, "nvme0", d0.Name)
	assert.Equal(t, "/dev/nvme0", d0.Node)
	assert.Equal(t, "Internal SSD 1TB", d0.Model)
	assert.Equal(t, "0000:c1:00.0", d0.Address)
	assert.Equal(t, "pcie", d0.Transport)
	require.Len(t, d0.Sensors, 2)

	comp, ok := d0.Composite()
	require.True(t, ok)
	assert.Equal(t, "Composite", comp.Label)
	assert.InDelta(t, 56.9, comp.Celsius, 0.001)
	assert.InDelta(t, 85.8, comp.WarnC, 0.001)
	assert.InDelta(t, 87.8, comp.CritC, 0.001)

	require.NotNil(t, d0.Throttle)
	assert.True(t, d0.Throttle.Throttled())
	assert.Equal(t, 3, d0.Throttle.Therm1TransCount)
	assert.Equal(t, 1, d0.Throttle.Therm2TransCount)
	assert.Equal(t, 7, d0.Throttle.WarningTempTime)

	d1 := snap.Drives[1]
	assert.Equal(t, "nvme1", d1.Name)
	require.Len(t, d1.Sensors, 3)
	require.NotNil(t, d1.Throttle)
	assert.False(t, d1.Throttle.Throttled())

	// Fans.
	require.Len(t, snap.Fans, 2)
	assert.Equal(t, "thinkpad", snap.Fans[0].Chip)
	assert.Equal(t, 3592, snap.Fans[0].RPM)
}

func TestCollectNoRunner(t *testing.T) {
	root := fakeSysfs(t)
	c := &Collector{SysfsRoot: root, Run: nil}
	snap, err := c.Collect()
	require.NoError(t, err)
	require.NotEmpty(t, snap.Drives)
	for _, d := range snap.Drives {
		assert.Nil(t, d.Throttle)
		assert.Error(t, d.SmartErr)
	}
}

func TestCollectSmartLogError(t *testing.T) {
	root := fakeSysfs(t)
	runner := func(string, ...string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	c := &Collector{SysfsRoot: root, Run: runner}
	snap, err := c.Collect()
	require.NoError(t, err)
	for _, d := range snap.Drives {
		assert.Nil(t, d.Throttle)
		assert.ErrorContains(t, d.SmartErr, "permission denied")
	}
}

func TestCollectMissingSysfs(t *testing.T) {
	c := &Collector{SysfsRoot: filepath.Join(t.TempDir(), "nope"), Run: nil}
	_, err := c.Collect()
	assert.Error(t, err)
}

func TestCompositeFallsBackToHottest(t *testing.T) {
	d := Drive{Sensors: []TempSensor{
		{Label: "Sensor 1", Celsius: 40},
		{Label: "Sensor 2", Celsius: 55},
	}}
	s, ok := d.Composite()
	require.True(t, ok)
	assert.Equal(t, "Sensor 2", s.Label)

	_, ok = Drive{}.Composite()
	assert.False(t, ok)
}

func TestThrottledFlag(t *testing.T) {
	assert.False(t, ThrottleStats{}.Throttled())
	assert.True(t, ThrottleStats{Therm1TransCount: 1}.Throttled())
	assert.True(t, ThrottleStats{WarningTempTime: 3}.Throttled())
}

func TestMilliToC(t *testing.T) {
	assert.InDelta(t, 56.9, milliToC(56900), 0.0001)
	assert.InDelta(t, 0, milliToC(0), 0.0001)
}
