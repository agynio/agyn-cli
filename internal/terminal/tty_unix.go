//go:build !windows

package terminal

import (
	"os"
	"os/signal"
	"syscall"
)

// WatchResize forwards SIGWINCH to the resize channel until Restore is called.
func (t *TTY) WatchResize() {
	if t == nil {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(signals)
		for {
			select {
			case <-t.stop:
				return
			case <-signals:
				if size, err := t.Size(); err == nil {
					t.emit(size)
				}
			}
		}
	}()
}
