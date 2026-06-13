package monitor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectBlockIO(t *testing.T) {
	root := fakeSysfs(t)
	c := &Collector{SysfsRoot: root, Run: nil}

	snap, err := c.Collect()
	require.NoError(t, err)
	require.Len(t, snap.Drives, 2)

	d0 := snap.Drives[0]
	require.NotNil(t, d0.IO)
	assert.Equal(t, uint64(2000), d0.IO.ReadSectors)
	assert.Equal(t, uint64(1000), d0.IO.WriteSectors)
	assert.Equal(t, uint64(500), d0.IO.IoMillis)
	assert.Equal(t, uint64(2000*512), d0.IO.ReadBytes())
	assert.Equal(t, uint64(1000*512), d0.IO.WriteBytes())

	// Capacity total derives from the raw namespace size (2_000_000 sectors).
	require.NotNil(t, d0.Capacity)
	assert.Equal(t, uint64(2_000_000*512), d0.Capacity.TotalBytes)
	// No mounts configured on this collector, so used is unknown.
	assert.False(t, d0.Capacity.UsedKnown)
}

func TestReadNamespacesSumsMultiple(t *testing.T) {
	dir := t.TempDir()
	ctrlDir := filepath.Join(dir, "nvme0")
	require.NoError(t, os.MkdirAll(filepath.Join(ctrlDir, "nvme0n1"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ctrlDir, "nvme0n2"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ctrlDir, "nvme0n1", "stat"),
		[]byte("0 0 100 0 0 0 50 0 0 10 0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ctrlDir, "nvme0n1", "size"),
		[]byte("1000\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ctrlDir, "nvme0n2", "stat"),
		[]byte("0 0 200 0 0 0 25 0 0 5 0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ctrlDir, "nvme0n2", "size"),
		[]byte("2000\n"), 0o644))

	info := readNamespaces(ctrlDir, "nvme0")
	require.NotNil(t, info.io)
	assert.ElementsMatch(t, []string{"nvme0n1", "nvme0n2"}, info.names)
	assert.Equal(t, uint64(300), info.io.ReadSectors)
	assert.Equal(t, uint64(75), info.io.WriteSectors)
	assert.Equal(t, uint64(15), info.io.IoMillis)
	assert.Equal(t, uint64(3000*512), info.sizeBytes)
}

func TestReadNamespacesNoneFound(t *testing.T) {
	info := readNamespaces(t.TempDir(), "nvme0")
	assert.Nil(t, info.io)
	assert.Empty(t, info.names)
	assert.Zero(t, info.sizeBytes)
}

func TestReadCapacityTotalOnly(t *testing.T) {
	// No mounts/statfs -> total known, used unknown.
	c := &Collector{}
	cap := c.readCapacity([]string{"nvme0n1"}, 1<<40)
	require.NotNil(t, cap)
	assert.Equal(t, uint64(1<<40), cap.TotalBytes)
	assert.False(t, cap.UsedKnown)
	assert.Equal(t, 0.0, cap.UsedFraction())

	// Zero device size -> no capacity at all.
	assert.Nil(t, c.readCapacity([]string{"nvme0n1"}, 0))
}

// directMountSysfs builds a sysfs tree where /dev/nvme0n1p2 is a plain
// partition of nvme0n1.
func directMountSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Plain partition leaf: no slaves dir.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "class", "block", "nvme0n1p2"), 0o755))
	return root
}

func TestReadCapacityDirectPartition(t *testing.T) {
	root := directMountSysfs(t)
	mounts := filepath.Join(root, "mounts")
	require.NoError(t, os.WriteFile(mounts, []byte(
		"/dev/nvme0n1p2 / ext4 rw 0 0\n"+
			"/dev/sda1 /mnt/other ext4 rw 0 0\n"+
			"tmpfs /run tmpfs rw 0 0\n"), 0o644))

	c := &Collector{
		SysfsRoot:  root,
		ProcMounts: mounts,
		Statfs: func(path string) (uint64, uint64, error) {
			assert.Equal(t, "/", path) // only the matching mount is statfs'd
			return 1000, 400, nil
		},
	}

	cap := c.readCapacity([]string{"nvme0n1"}, 999_999) // total from raw size
	require.NotNil(t, cap)
	assert.Equal(t, uint64(999_999), cap.TotalBytes)
	assert.True(t, cap.UsedKnown)
	assert.Equal(t, uint64(400), cap.UsedBytes)
}

// dmSysfs builds a sysfs tree modeling LVM-on-LUKS:
//
//	dm-1 (vg-root) -> dm-0 (luks) -> nvme0n1p3
func dmSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	blk := filepath.Join(root, "class", "block")

	// dm-0: luks, slave = nvme0n1p3
	require.NoError(t, os.MkdirAll(filepath.Join(blk, "dm-0", "dm"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(blk, "dm-0", "dm", "name"), []byte("luks-abc\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(blk, "dm-0", "slaves", "nvme0n1p3"), 0o755))

	// dm-1: vg-root, slave = dm-0
	require.NoError(t, os.MkdirAll(filepath.Join(blk, "dm-1", "dm"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(blk, "dm-1", "dm", "name"), []byte("vg-root\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(blk, "dm-1", "slaves", "dm-0"), 0o755))

	// Physical partition leaf.
	require.NoError(t, os.MkdirAll(filepath.Join(blk, "nvme0n1p3"), 0o755))
	return root
}

// TestReadCapacityBtrfsSubvolumesCountedOnce ensures the same filesystem
// mounted at several mountpoints (btrfs subvolumes / bind mounts) is not
// double-counted, which previously produced >100% usage.
func TestReadCapacityBtrfsSubvolumesCountedOnce(t *testing.T) {
	root := directMountSysfs(t)
	mounts := filepath.Join(root, "mounts")
	require.NoError(t, os.WriteFile(mounts, []byte(
		"/dev/nvme0n1p2 / btrfs rw,subvol=/root 0 0\n"+
			"/dev/nvme0n1p2 /home btrfs rw,subvol=/home 0 0\n"+
			"/dev/nvme0n1p2 /var btrfs rw,subvol=/var 0 0\n"), 0o644))

	calls := 0
	c := &Collector{
		SysfsRoot:  root,
		ProcMounts: mounts,
		Statfs: func(string) (uint64, uint64, error) {
			calls++
			return 1_000_000, 600_000, nil // whole-fs figures, repeated per subvol
		},
	}

	cap := c.readCapacity([]string{"nvme0n1"}, 1_000_000)
	require.NotNil(t, cap)
	assert.Equal(t, 1, calls, "filesystem should be statfs'd once")
	assert.Equal(t, uint64(600_000), cap.UsedBytes, "used must not be multiplied by subvolume count")
	assert.InDelta(t, 0.6, cap.UsedFraction(), 1e-9)
}

func TestUsedFractionClamped(t *testing.T) {
	// Even if used somehow exceeds total, the fraction never exceeds 1.
	c := Capacity{TotalBytes: 1000, UsedBytes: 1600, UsedKnown: true}
	assert.Equal(t, 1.0, c.UsedFraction())
}

func TestReadCapacityThroughDeviceMapper(t *testing.T) {
	root := dmSysfs(t)
	mounts := filepath.Join(root, "mounts")
	require.NoError(t, os.WriteFile(mounts, []byte(
		"/dev/mapper/vg-root / ext4 rw 0 0\n"), 0o644))

	c := &Collector{
		SysfsRoot:  root,
		ProcMounts: mounts,
		Statfs: func(string) (uint64, uint64, error) {
			return 900_000, 350_000, nil
		},
	}

	cap := c.readCapacity([]string{"nvme0n1"}, 1_000_000)
	require.NotNil(t, cap)
	assert.True(t, cap.UsedKnown)
	assert.Equal(t, uint64(350_000), cap.UsedBytes)
	assert.Equal(t, uint64(1_000_000), cap.TotalBytes)
}

func TestMountOnDriveExcludesOtherDrive(t *testing.T) {
	root := dmSysfs(t) // dm chain lands on nvme0n1p3
	c := &Collector{SysfsRoot: root}
	dmNames := c.dmNameMap()

	// nvme1's namespaces should NOT match a mount backed by nvme0.
	assert.False(t, c.mountOnDrive("/dev/mapper/vg-root", map[string]bool{"nvme1n1": true}, dmNames))
	assert.True(t, c.mountOnDrive("/dev/mapper/vg-root", map[string]bool{"nvme0n1": true}, dmNames))
}

func TestBlockName(t *testing.T) {
	c := &Collector{}
	dmNames := map[string]string{"vg-root": "dm-3"}
	assert.Equal(t, "nvme0n1p2", c.blockName("/dev/nvme0n1p2", dmNames))
	assert.Equal(t, "dm-3", c.blockName("/dev/mapper/vg-root", dmNames))
	assert.Equal(t, "", c.blockName("/dev/mapper/unknown", dmNames))
	assert.Equal(t, "", c.blockName("tmpfs", dmNames))
}

func TestPhysicalLeavesReducesPartition(t *testing.T) {
	root := directMountSysfs(t)
	c := &Collector{SysfsRoot: root}
	// nvme0n1p2 has no slaves -> reduced to namespace nvme0n1.
	assert.Equal(t, []string{"nvme0n1"}, c.physicalLeaves("nvme0n1p2", 0))
	// A non-nvme leaf passes through unchanged.
	assert.Equal(t, []string{"sda1"}, c.physicalLeaves("sda1", 0))
}

func TestUnescapeMount(t *testing.T) {
	assert.Equal(t, "/home dir", unescapeMount(`/home\040dir`))
	assert.Equal(t, "/simple", unescapeMount("/simple"))
	assert.Equal(t, "/a\tb", unescapeMount(`/a\011b`))
	assert.Equal(t, `/trailing\`, unescapeMount(`/trailing\`))
}

func TestCapacityUsedFraction(t *testing.T) {
	assert.Equal(t, 0.0, Capacity{}.UsedFraction())
	assert.Equal(t, 0.0, Capacity{TotalBytes: 100, UsedBytes: 50}.UsedFraction()) // UsedKnown false
	assert.InDelta(t, 0.5, Capacity{TotalBytes: 100, UsedBytes: 50, UsedKnown: true}.UsedFraction(), 1e-9)
}
