//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package fontinspect

import "os"

func openInput(path string) (*os.File, error) {
	return os.Open(path)
}
