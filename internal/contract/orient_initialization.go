package contract

import (
	"errors"
	"fmt"
	"os"
)

// OrientationInitializationError identifies filesystem denial without guessing its
// sandbox, OS, or ACL source, and preserves the original operation and path.
func OrientationInitializationError(stage string, err error) error {
	if !errors.Is(err, os.ErrPermission) {
		return err
	}
	return fmt.Errorf("orientation %s: filesystem access denied: %w", stage, err)
}
