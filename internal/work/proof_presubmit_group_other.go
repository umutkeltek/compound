//go:build !darwin && !linux

package work

import (
	"fmt"
	"runtime"
)

func presubmitGroupContainmentSupported() error {
	return fmt.Errorf("native presubmit exact process-group containment is unsupported on %s", runtime.GOOS)
}
func observePresubmitOwnedGroup(int) (bool, error) {
	return false, presubmitGroupContainmentSupported()
}

func presubmitRecordedGroupMembers(int) (int, error) {
	return 0, presubmitGroupContainmentSupported()
}

func newPresubmitOwnedProcessGroup(int) (presubmitOwnedProcessGroup, error) {
	return nil, presubmitGroupContainmentSupported()
}
