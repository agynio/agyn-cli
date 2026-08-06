//go:build linux

package cmd

import "syscall"

// redirectStdoutFD points the real file descriptor 1 at stderr. Linux on arm64
// has no dup2, so this is Dup3 with no flags.
func redirectStdoutFD() error {
	return syscall.Dup3(int(2), int(1), 0)
}
