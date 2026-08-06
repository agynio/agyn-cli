//go:build unix

package endpoint

import "syscall"

// availableBytes reports free space on the filesystem holding path. Staging is
// refused before it starts rather than discovered as a write failure partway
// through a transfer.
func availableBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
