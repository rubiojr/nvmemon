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
}

func TestReadNamespaceIOSumsMultiple(t *testing.T) {
	dir := t.TempDir()
	ctrlDir := filepath.Join(dir, "nvme0")
	require.NoError(t, os.MkdirAll(filepath.Join(ctrlDir, "nvme0n1"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ctrlDir, "nvme0n2"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ctrlDir, "nvme0n1", "stat"),
		[]byte("0 0 100 0 0 0 50 0 0 10 0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ctrlDir, "nvme0n2", "stat"),
		[]byte("0 0 200 0 0 0 25 0 0 5 0\n"), 0o644))

	names, io := readNamespaceIO(ctrlDir, "nvme0")
	require.NotNil(t, io)
	assert.ElementsMatch(t, []string{"nvme0n1", "nvme0n2"}, names)
	assert.Equal(t, uint64(300), io.ReadSectors)
	assert.Equal(t, uint64(75), io.WriteSectors)
	assert.Equal(t, uint64(15), io.IoMillis)
}

func TestReadNamespaceIONoneFound(t *testing.T) {
	dir := t.TempDir()
	names, io := readNamespaceIO(dir, "nvme0")
	assert.Nil(t, io)
	assert.Empty(t, names)
}

func TestReadCapacity(t *testing.T) {
	dir := t.TempDir()
	mounts := filepath.Join(dir, "mounts")
	require.NoError(t, os.WriteFile(mounts, []byte(
		"/dev/nvme0n1p1 /boot ext4 rw 0 0\n"+
			"/dev/nvme0n1p2 /home\\040dir ext4 rw 0 0\n"+
			"/dev/sda1 /mnt/other ext4 rw 0 0\n"+
			"tmpfs /run tmpfs rw 0 0\n"), 0o644))

	statfsCalls := map[string]bool{}
	c := &Collector{
		ProcMounts: mounts,
		Statfs: func(path string) (uint64, uint64, error) {
			statfsCalls[path] = true
			return 100, 40, nil // each mount: 100 total, 40 used
		},
	}

	cap := c.readCapacity([]string{"nvme0n1"})
	require.NotNil(t, cap)
	// Two matching mounts (p1, p2) summed; sda1 and tmpfs excluded.
	assert.Equal(t, uint64(200), cap.TotalBytes)
	assert.Equal(t, uint64(80), cap.UsedBytes)
	assert.InDelta(t, 0.4, cap.UsedFraction(), 1e-9)
	// Octal-escaped mountpoint was decoded before statfs.
	assert.True(t, statfsCalls["/home dir"])
	assert.False(t, statfsCalls["/mnt/other"])
}

func TestReadCapacityDisabled(t *testing.T) {
	assert.Nil(t, (&Collector{}).readCapacity([]string{"nvme0n1"}))

	c := &Collector{ProcMounts: "x", Statfs: func(string) (uint64, uint64, error) { return 1, 1, nil }}
	assert.Nil(t, c.readCapacity(nil)) // no namespaces
}

func TestDeviceBelongs(t *testing.T) {
	ns := []string{"nvme0n1"}
	assert.True(t, deviceBelongs("/dev/nvme0n1", ns))
	assert.True(t, deviceBelongs("/dev/nvme0n1p3", ns))
	assert.False(t, deviceBelongs("/dev/nvme0n2", ns))
	assert.False(t, deviceBelongs("/dev/sda1", ns))
}

func TestUnescapeMount(t *testing.T) {
	assert.Equal(t, "/home dir", unescapeMount(`/home\040dir`))
	assert.Equal(t, "/simple", unescapeMount("/simple"))
	assert.Equal(t, "/a\tb", unescapeMount(`/a\011b`))
	assert.Equal(t, `/trailing\`, unescapeMount(`/trailing\`))
}

func TestCapacityUsedFractionZero(t *testing.T) {
	assert.Equal(t, 0.0, Capacity{}.UsedFraction())
}
