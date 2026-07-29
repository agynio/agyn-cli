//go:build windows

package terminal

// WatchResize is a no-op on Windows, which has no SIGWINCH. The remote PTY
// keeps the size sent in the handshake.
func (t *TTY) WatchResize() {}
