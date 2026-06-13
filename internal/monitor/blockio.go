package monitor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sectorBytes is the fixed unit used by the kernel block-stat and size
// interfaces, regardless of the device's physical/logical block size.
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

// Capacity describes a drive's size and how much of it holds data.
type Capacity struct {
	// TotalBytes is the raw device capacity reported by sysfs.
	TotalBytes uint64
	// UsedBytes is the bytes used across filesystems backed by this drive.
	UsedBytes uint64
	// UsedKnown reports whether UsedBytes could be determined. It is false
	// when no filesystem backed by the drive is mounted (or could be resolved
	// through device-mapper), in which case only the total is meaningful.
	UsedKnown bool
}

// UsedFraction returns used/total in [0,1], or 0 when unknown.
func (c Capacity) UsedFraction() float64 {
	if !c.UsedKnown || c.TotalBytes == 0 {
		return 0
	}
	f := float64(c.UsedBytes) / float64(c.TotalBytes)
	if f > 1 {
		return 1
	}
	return f
}

// StatfsFunc reports the total and used bytes of the filesystem mounted at
// path. It is injectable for testing.
type StatfsFunc func(path string) (total, used uint64, err error)

// nvmePartRe matches an NVMe partition (e.g. nvme0n1p3) so it can be reduced to
// its parent namespace (nvme0n1).
var nvmePartRe = regexp.MustCompile(`^(nvme\d+n\d+)p\d+$`)

// attachBlockIO populates d.IO and d.Capacity from the drive's namespaces.
func (c *Collector) attachBlockIO(d *Drive, devPath string) {
	ns := readNamespaces(devPath, d.Name)
	d.IO = ns.io
	if cap := c.readCapacity(ns.names, ns.sizeBytes); cap != nil {
		d.Capacity = cap
	}
}

// namespaceInfo aggregates what we learn from a controller's namespaces.
type namespaceInfo struct {
	names     []string // namespace device names, e.g. ["nvme0n1"]
	io        *IOCounters
	sizeBytes uint64 // raw capacity summed across namespaces
}

// readNamespaces reads block stats and raw size for every namespace under a
// controller directory.
func readNamespaces(devPath, ctrl string) namespaceInfo {
	// Namespaces appear as subdirectories named <ctrl>nN (e.g. nvme0n1).
	matches, _ := filepath.Glob(filepath.Join(devPath, ctrl+"n*"))

	var info namespaceInfo
	var acc IOCounters
	haveIO := false
	for _, ns := range matches {
		name := filepath.Base(ns)
		info.names = append(info.names, name)

		// Raw capacity: size is in 512-byte sectors.
		if sectors, ok := parseUint(readStr(filepath.Join(ns, "size"))); ok {
			info.sizeBytes += sectors * sectorBytes
		}

		// Block stat layout: field[2]=read sectors, [6]=write sectors,
		// [9]=io_ticks (ms busy).
		fields := strings.Fields(readStr(filepath.Join(ns, "stat")))
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
		haveIO = true
	}
	if haveIO {
		info.io = &acc
	}
	return info
}

// readCapacity reports the drive's capacity. Total comes from the raw device
// size; used is summed from filesystems backed by the drive's namespaces,
// resolving through device-mapper (LVM/LUKS/mdraid) layers.
func (c *Collector) readCapacity(nsNames []string, totalBytes uint64) *Capacity {
	if totalBytes == 0 {
		return nil
	}
	cap := &Capacity{TotalBytes: totalBytes}

	if c.ProcMounts == "" || c.Statfs == nil || len(nsNames) == 0 {
		return cap // total only; used unknown
	}
	data, err := os.ReadFile(c.ProcMounts)
	if err != nil {
		return cap
	}

	nsSet := map[string]bool{}
	for _, n := range nsNames {
		nsSet[n] = true
	}
	dmNames := c.dmNameMap()

	// Dedup by the resolved source block device, not the mountpoint: the same
	// filesystem can be mounted at many mountpoints (btrfs subvolumes, bind
	// mounts) and statfs reports identical usage for each, which would
	// otherwise be counted multiple times. Distinct partitions/LVs on the
	// drive have distinct block devices, so they still sum correctly.
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		c.addMountUsage(line, nsSet, dmNames, seen, cap)
	}
	return cap
}

// addMountUsage adds one mount-table line's filesystem usage to cap when the
// mount is backed by the drive and not already counted.
func (c *Collector) addMountUsage(line string, nsSet map[string]bool, dmNames map[string]string, seen map[string]bool, cap *Capacity) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return
	}
	dev, mountpoint := f[0], unescapeMount(f[1])
	block := c.blockName(dev, dmNames)
	if block == "" || seen[block] || !c.blockOnDrive(block, nsSet) {
		return
	}
	seen[block] = true
	if _, used, err := c.Statfs(mountpoint); err == nil {
		cap.UsedBytes += used
		cap.UsedKnown = true
	}
}

// mountOnDrive reports whether a mount source device resides (possibly through
// device-mapper) on one of the drive's namespaces.
func (c *Collector) mountOnDrive(dev string, nsSet map[string]bool, dmNames map[string]string) bool {
	block := c.blockName(dev, dmNames)
	if block == "" {
		return false
	}
	return c.blockOnDrive(block, nsSet)
}

// blockOnDrive reports whether a kernel block device resolves (through any
// device-mapper layers) to one of the drive's namespaces.
func (c *Collector) blockOnDrive(block string, nsSet map[string]bool) bool {
	for _, leaf := range c.physicalLeaves(block, 0) {
		if nsSet[leaf] {
			return true
		}
	}
	return false
}

// blockName maps a mount source device path to its kernel block device name
// (e.g. /dev/nvme0n1p2 -> nvme0n1p2, /dev/mapper/vg-root -> dm-0). Non-block
// sources (tmpfs, proc, ...) return "".
func (c *Collector) blockName(dev string, dmNames map[string]string) string {
	if strings.HasPrefix(dev, "/dev/mapper/") {
		if name, ok := dmNames[strings.TrimPrefix(dev, "/dev/mapper/")]; ok {
			return name
		}
		return ""
	}
	if strings.HasPrefix(dev, "/dev/") {
		return filepath.Base(dev)
	}
	return ""
}

// physicalLeaves walks device-mapper "slaves" links to find the underlying
// physical block devices for a kernel block name, reducing NVMe partitions to
// their namespace. depth guards against pathological cycles.
func (c *Collector) physicalLeaves(block string, depth int) []string {
	if depth > 16 {
		return nil
	}
	slavesDir := filepath.Join(c.SysfsRoot, "class", "block", block, "slaves")
	entries, err := os.ReadDir(slavesDir)
	if err == nil && len(entries) > 0 {
		var leaves []string
		for _, e := range entries {
			leaves = append(leaves, c.physicalLeaves(e.Name(), depth+1)...)
		}
		return leaves
	}
	// Leaf device: reduce an NVMe partition to its namespace.
	if m := nvmePartRe.FindStringSubmatch(block); m != nil {
		return []string{m[1]}
	}
	return []string{block}
}

// dmNameMap returns a map from device-mapper name (as under /dev/mapper) to its
// kernel block name (dm-N), built from sysfs.
func (c *Collector) dmNameMap() map[string]string {
	out := map[string]string{}
	dms, _ := filepath.Glob(filepath.Join(c.SysfsRoot, "class", "block", "dm-*"))
	for _, dm := range dms {
		name := strings.TrimSpace(readStr(filepath.Join(dm, "dm", "name")))
		if name != "" {
			out[name] = filepath.Base(dm)
		}
	}
	return out
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
