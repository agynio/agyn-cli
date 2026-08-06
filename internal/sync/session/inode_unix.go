//go:build unix

package session

import (
	"fmt"
	"os"
	"syscall"
)

// RootInode identifies a directory by its inode. A directory deleted and
// recreated gets a new one, an unmounted drive exposes the mountpoint
// underneath, and a mountpoint that never mounted was never the same directory
// — all three would otherwise scan as an empty tree.
func RootInode(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot read the inode of %s on this platform", path)
	}
	return uint64(stat.Ino), nil
}
