//go:build !windows

package filecommit

import "os"

// MoveNoReplace exposes a completed temporary file at destination without
// replacing a path created by another process. Source and destination must be
// on the same filesystem.
func MoveNoReplace(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	_ = os.Remove(source)
	return nil
}
