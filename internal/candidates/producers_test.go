package candidates

import "testing"

// The automatic producers are off unless the switch is exactly "1"; a typo or
// "true" must not silently turn the pile back on.
func TestAutomaticProducersOptIn(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "true": false, "yes": false, "1": true, " 1 ": true} {
		t.Setenv(ProducersEnv, value)
		if got := AutomaticProducersEnabled(); got != want {
			t.Fatalf("%s=%q: enabled=%v, want %v", ProducersEnv, value, got, want)
		}
	}
	if ValidBasis(BasisLessonOnWrite) {
		t.Fatal("lesson_on_write must not be a candidate promotion basis: the driver would auto-promote candidates that claim it")
	}
}
