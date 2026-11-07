//go:build unix

package fontinstall

import (
	"os"
	"syscall"
)

// Nonblocking open lets the caller reject FIFOs before attempting a read.
func openNonblocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
