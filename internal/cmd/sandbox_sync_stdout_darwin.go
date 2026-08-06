//go:build darwin

package cmd

import "syscall"

// redirectStdoutFD points the real file descriptor 1 at stderr.
func redirectStdoutFD() error {
	return syscall.Dup2(int(2), int(1))
}
