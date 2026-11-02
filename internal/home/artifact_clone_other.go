//go:build !darwin

package home

import (
	"errors"
	"os"
)

// CloneFileInto is unavailable here; callers copy instead.
func CloneFileInto(_ *os.File, _, _ string) error {
	return errors.ErrUnsupported
}
