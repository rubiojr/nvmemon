package monitor

import "golang.org/x/sys/unix"

// unixStatfs is the default StatfsFunc, reporting total and used bytes for the
// filesystem mounted at path using the statfs(2) syscall.
func unixStatfs(path string) (total, used uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	// Used = total blocks minus free blocks (df-style, counting reserved space
	// as used so the figure matches the on-disk footprint).
	used = (st.Blocks - st.Bfree) * bsize
	return total, used, nil
}
