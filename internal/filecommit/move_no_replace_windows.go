//go:build windows

package filecommit

import "golang.org/x/sys/windows"

// MoveNoReplace exposes a completed temporary file at destination without
// replacing a path created by another process.
func MoveNoReplace(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
