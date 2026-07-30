package activity

import (
	"strings"
	"testing"
)

const okLine = `{"schema_version":"compound.activity_observation.v0.2","seq":1,"ts":"2026-07-15T10:00:00Z","kind":"app_activated","bundle_id":"com.apple.dt.Xcode","app_name":"Xcode","engine":{"id":"compound-activity-sensor","revision":"v0.1"}}`

// TestFalsifier1_UnknownFieldRefused proves a line carrying a field outside the closed schema (here a
// smuggled "title") is REFUSED and counted, never accepted as a silent extra. A naive
// json.Unmarshal accepts and drops the unknown field — this test reds against that.
func TestFalsifier1_UnknownFieldRefused(t *testing.T) {
	bad := `{"schema_version":"compound.activity_observation.v0.2","seq":2,"ts":"2026-07-15T10:01:00Z","kind":"app_activated","bundle_id":"com.apple.Safari","app_name":"Safari","engine":{"id":"e","revision":"v0.1"},"title":"secret document name"}`
	rep := DecodeStrict([]byte(okLine+"\n"+bad), 1)
	if len(rep.Accepted) != 1 {
		t.Fatalf("expected exactly the 1 clean event accepted, got %d", len(rep.Accepted))
	}
	if len(rep.Refused) != 1 {
		t.Fatalf("expected the unknown-field line refused+counted, got %d refusals", len(rep.Refused))
	}
	if rep.Refused[0].Line != 2 {
		t.Fatalf("refused line number = %d, want 2", rep.Refused[0].Line)
	}
	// The reason must not leak the smuggled value.
	if strings.Contains(rep.Refused[0].Reason, "secret document name") {
		t.Fatalf("refusal reason leaked the payload: %q", rep.Refused[0].Reason)
	}
}

// TestFalsifier1_UnknownKindRefused proves an event with a kind outside the closed vocabulary is
// refused (naive decode accepts any string).
func TestFalsifier1_UnknownKindRefused(t *testing.T) {
	bad := `{"schema_version":"compound.activity_observation.v0.2","seq":3,"ts":"2026-07-15T10:02:00Z","kind":"keystroke_logged","bundle_id":"com.apple.Safari","app_name":"Safari","engine":{"id":"e","revision":"v0.1"}}`
	rep := DecodeStrict([]byte(bad), 1)
	if len(rep.Accepted) != 0 {
		t.Fatalf("unknown kind must be refused, got %d accepted", len(rep.Accepted))
	}
	if len(rep.Refused) != 1 || !strings.Contains(rep.Refused[0].Reason, "unknown kind") {
		t.Fatalf("want one 'unknown kind' refusal, got %+v", rep.Refused)
	}
}

// TestFalsifier5_ForbiddenMetaKeyRefused proves that even a schema-shaped event is refused if its
// meta map carries a content-bearing key — belt-and-braces on top of the content-free schema.
func TestFalsifier5_ForbiddenMetaKeyRefused(t *testing.T) {
	for _, key := range []string{"title", "url", "document", "text", "content"} {
		bad := `{"schema_version":"compound.activity_observation.v0.2","seq":4,"ts":"2026-07-15T10:03:00Z","kind":"app_activated","bundle_id":"com.apple.Safari","app_name":"Safari","engine":{"id":"e","revision":"v0.1"},"meta":{"` + key + `":"leak"}}`
		rep := DecodeStrict([]byte(bad), 1)
		if len(rep.Accepted) != 0 {
			t.Fatalf("meta key %q must be refused, got accepted", key)
		}
		if len(rep.Refused) != 1 || !strings.Contains(rep.Refused[0].Reason, "forbidden content meta key") {
			t.Fatalf("meta key %q: want forbidden-meta refusal, got %+v", key, rep.Refused)
		}
	}
}

// TestSchemaAcceptsCleanEvent guards the happy path so the strict gate does not over-reject.
func TestSchemaAcceptsCleanEvent(t *testing.T) {
	rep := DecodeStrict([]byte(okLine), 1)
	if len(rep.Refused) != 0 {
		t.Fatalf("clean event refused: %+v", rep.Refused)
	}
	if len(rep.Accepted) != 1 || rep.Accepted[0].Kind != KindAppActivated {
		t.Fatalf("clean event not accepted: %+v", rep.Accepted)
	}
	// A bounded, non-content meta key is allowed.
	withMeta := `{"schema_version":"compound.activity_observation.v0.2","seq":5,"ts":"2026-07-15T10:04:00Z","kind":"idle_started","bundle_id":"","app_name":"","engine":{"id":"e","revision":"v0.1"},"meta":{"idle_sec":"120"}}`
	rep2 := DecodeStrict([]byte(withMeta), 1)
	if len(rep2.Refused) != 0 || len(rep2.Accepted) != 1 {
		t.Fatalf("idle event with bounded meta should pass: accepted=%d refused=%+v", len(rep2.Accepted), rep2.Refused)
	}
}

// TestSchemaRefusesBadTimestampAndVersion covers the remaining strict gates.
func TestSchemaRefusesBadTimestampAndVersion(t *testing.T) {
	badTS := `{"schema_version":"compound.activity_observation.v0.2","seq":6,"ts":"not-a-time","kind":"app_activated","bundle_id":"com.apple.Safari","app_name":"Safari","engine":{"id":"e","revision":"v0.1"}}`
	if rep := DecodeStrict([]byte(badTS), 1); len(rep.Accepted) != 0 || len(rep.Refused) != 1 {
		t.Fatalf("bad ts must refuse: %+v", rep)
	}
	badVer := `{"schema_version":"compound.activity_observation.v9.9","seq":7,"ts":"2026-07-15T10:05:00Z","kind":"app_activated","bundle_id":"com.apple.Safari","app_name":"Safari","engine":{"id":"e","revision":"v0.1"}}`
	if rep := DecodeStrict([]byte(badVer), 1); len(rep.Accepted) != 0 || len(rep.Refused) != 1 {
		t.Fatalf("bad schema_version must refuse: %+v", rep)
	}
}
