//go:build darwin

package home

import (
	"os"

	"golang.org/x/sys/unix"
)

// CloneFileInto makes an APFS clone of source at dir/name in one atomic step.
// The clone holds the exact bytes source held at that moment and shares their
// blocks until either file changes, so successive versions of an append-only
// file cost only their new bytes. It fails across volumes; callers then copy.
func CloneFileInto(source *os.File, dir, name string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return unix.Fclonefileat(int(source.Fd()), int(handle.Fd()), name, 0)
}
