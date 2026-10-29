package contract

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestOrientationInitializationErrorPreservesPermissionIdentity(t *testing.T) {
	stages := []string{
		"configure memory reader",
		"load orientation policy",
		"materialize runtime context",
		"materialize context bodies",
		"persist runtime context",
	}
	for _, stage := range stages {
		for _, tc := range []struct {
			name  string
			cause error
		}{
			{"operation not permitted", syscall.EPERM},
			{"permission denied", syscall.EACCES},
			{"permission sentinel", os.ErrPermission},
		} {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				pathErr := &os.PathError{Op: "open", Path: "synthetic/mem.lock", Err: tc.cause}
				original := fmt.Errorf("read context: %w", pathErr)
				got := OrientationInitializationError(stage, original)
				want := "orientation " + stage + ": filesystem access denied: " + original.Error()
				if got == original || got.Error() != want {
					t.Fatalf("permission diagnosis = %v, want %q", got, want)
				}
				if !errors.Is(got, tc.cause) || !errors.Is(got, original) {
					t.Fatalf("error identity lost: %v", got)
				}
				var preserved *os.PathError
				if !errors.As(got, &preserved) || preserved != pathErr {
					t.Fatalf("operation/path identity lost: %v", got)
				}
			})
		}
	}
}

func TestOrientationInitializationErrorPreservesNonPermissionErrors(t *testing.T) {
	pathErr := &os.PathError{Op: "open", Path: "synthetic/mem.lock", Err: syscall.EWOULDBLOCK}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"would block", syscall.EWOULDBLOCK},
		{"wrapped would block", fmt.Errorf("read context: %w", pathErr)},
		{"malformed data", errors.New("invalid input record")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := OrientationInitializationError("materialize runtime context", tc.err); got != tc.err {
				t.Fatalf("non-permission error reclassified: got %v, want original %v", got, tc.err)
			}
		})
	}
}
