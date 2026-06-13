package monitor

import (
	"os"
	"path/filepath"
	"strings"
)

// sectorBytes is the fixed unit used by the kernel block-stat interface,
// regardless of the device's physical/logical block size.
const sectorBytes = 512

// IOCounters holds cumulative block-layer counters for a drive, summed across
// all of its namespaces. Values are monotonic since boot.
type IOCounters struct {
	ReadSectors  uint64 // 512-byte sectors read
	WriteSectors uint64 // 512-byte sectors written
	IoMillis     uint64 // milliseconds the device spent doing I/O (busy time)
}

// ReadBytes returns total bytes read.
func (c IOCounters) ReadBytes() uint64 { return c.ReadSectors * sectorBytes }

// WriteBytes returns total bytes written.
func (c IOCounters) WriteBytes() uint64 { return c.WriteSectors * sectorBytes }

// Capacity holds aggregate filesystem usage for a drive.
type Capacity struct {
	UsedBytes  uint64
	TotalBytes uint64
}

// UsedFraction returns used/total in [0,1], or 0 when total is unknown.
func (c Capacity) UsedFraction() float64 {
	if c.TotalBytes == 0 {
		return 0
	}
	return float64(c.UsedBytes) / float64(c.TotalBytes)
}

// StatfsFunc reports the total and used bytes of the filesystem mounted at
// path. It is injectable for testing.
type StatfsFunc func(path string) (total, used uint64, err error)

// attachBlockIO populates d.IO and d.Capacity from the drive's namespaces.
func (c *Collector) attachBlockIO(d *Drive, devPath string) {
	nsNames, io := readNamespaceIO(devPath, d.Name)
	d.IO = io
	if cap := c.readCapacity(nsNames); cap != nil {
		d.Capacity = cap
	}
}

// readNamespaceIO sums the block stats of every namespace under a controller
// directory and returns the namespace device names it found.
func readNamespaceIO(devPath, ctrl string) (names []string, io *IOCounters) {
	// Namespaces appear as subdirectories named <ctrl>nN (e.g. nvme0n1).
	matches, _ := filepath.Glob(filepath.Join(devPath, ctrl+"n*"))
	var acc IOCounters
	found := false
	for _, ns := range matches {
		statPath := filepath.Join(ns, "stat")
		fields := strings.Fields(readStr(statPath))
		// Block stat layout: field[2]=read sectors, [6]=write sectors,
		// [9]=io_ticks (ms busy).
		if len(fields) < 10 {
			continue
		}
		rd, ok1 := parseUint(fields[2])
		wr, ok2 := parseUint(fields[6])
		io9, ok3 := parseUint(fields[9])
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		acc.ReadSectors += rd
		acc.WriteSectors += wr
		acc.IoMillis += io9
		names = append(names, filepath.Base(ns))
		found = true
	}
	if !found {
		return names, nil
	}
	return names, &acc
}

// readCapacity sums filesystem usage of every mount backed by one of the given
// namespace devices (including their partitions).
func (c *Collector) readCapacity(nsNames []string) *Capacity {
	if c.ProcMounts == "" || c.Statfs == nil || len(nsNames) == 0 {
		return nil
	}
	data, err := os.ReadFile(c.ProcMounts)
	if err != nil {
		return nil
	}

	seen := map[string]bool{} // dedupe by mountpoint
	var cap Capacity
	any := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		dev, mountpoint := f[0], unescapeMount(f[1])
		if !deviceBelongs(dev, nsNames) || seen[mountpoint] {
			continue
		}
		seen[mountpoint] = true
		total, used, err := c.Statfs(mountpoint)
		if err != nil || total == 0 {
			continue
		}
		cap.TotalBytes += total
		cap.UsedBytes += used
		any = true
	}
	if !any {
		return nil
	}
	return &cap
}

// deviceBelongs reports whether a mount device path (e.g. /dev/nvme0n1p2)
// belongs to one of the namespace devices (e.g. nvme0n1).
func deviceBelongs(dev string, nsNames []string) bool {
	base := filepath.Base(dev)
	for _, ns := range nsNames {
		if base == ns || strings.HasPrefix(base, ns+"p") {
			return true
		}
	}
	return false
}

// unescapeMount decodes the octal escapes (\040 etc.) used in /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, ok := parseOctal(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseOctal(s string) (byte, bool) {
	var v int
	for _, r := range s {
		if r < '0' || r > '7' {
			return 0, false
		}
		v = v*8 + int(r-'0')
	}
	if v > 255 {
		return 0, false
	}
	return byte(v), true
}
