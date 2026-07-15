package eventstore

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameCanonicalizesAndSelfValidates(t *testing.T) {
	record := []byte("{\n  \"schema_version\": \"compound.receipt.v0.1\",\n  \"id\": \"rec_frame\",\n  \"created_at\": \"2026-07-15T00:00:00Z\",\n  \"value\": {\"b\": 2, \"a\": 1}\n}\n")
	frame, canonicalRecord, err := buildFrame("rec_frame", record)
	if err != nil {
		t.Fatal(err)
	}
	if frame.FrameVersion != 1 || frame.Family != "receipt" || frame.ID != "rec_frame" {
		t.Fatalf("unexpected frame header: %+v", frame)
	}
	if bytes.Contains(canonicalRecord, []byte("\n")) {
		t.Fatalf("record was not compacted: %q", canonicalRecord)
	}
	line, err := encodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(line, []byte("\n")) != 1 || line[len(line)-1] != '\n' {
		t.Fatalf("frame is not exactly one JSONL line: %q", line)
	}
	decoded, err := decodeFrame(bytes.TrimSuffix(line, []byte("\n")))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SHA256 != frame.SHA256 || !bytes.Equal(decoded.Record, canonicalRecord) {
		t.Fatalf("decoded frame differs: got=%+v canonical=%s", decoded, canonicalRecord)
	}
}

func TestFrameRejectsIdentityAndChecksumDrift(t *testing.T) {
	if _, _, err := buildFrame("rec_arg", []byte(`{"id":"rec_body"}`)); err == nil {
		t.Fatal("embedded id mismatch unexpectedly accepted")
	}
	frame, _, err := buildFrame("rec_checksum", []byte(`{"id":"rec_checksum","schema_version":"compound.receipt.v0.1","created_at":"2026-07-15T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	frame.SHA256 = "00"
	if err := validateFrame(frame); err == nil {
		t.Fatal("checksum drift unexpectedly accepted")
	} else {
		var invalid *FrameInvalidError
		if !errors.As(err, &invalid) {
			t.Fatalf("checksum error type = %T, want *FrameInvalidError: %v", err, err)
		}
	}
}

func TestFamilyUsesSchemaBeforeIDPrefixAndFallsBackToPrefix(t *testing.T) {
	if got := familyOf([]byte(`{"schema_version":"compound.transition.v0.1"}`), "rec_misleading"); got != "transition" {
		t.Fatalf("schema family = %q, want transition", got)
	}
	if got := familyOf([]byte(`{"id":"hook_fallback"}`), "hook_fallback"); got != "hook" {
		t.Fatalf("prefix family = %q, want hook", got)
	}
	if got := familyOf([]byte(`{"id":"unknown"}`), "unknown"); got != "other" {
		t.Fatalf("unknown family = %q, want other", got)
	}
}
