//go:build !linux && !darwin

package cmd

// redirectStdoutFD is a no-op where the descriptor cannot be moved. Reassigning
// os.Stdout still covers every Go writer, which is all this binary has.
func redirectStdoutFD() error { return nil }
