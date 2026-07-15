package eventstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const frameVersion = 1

// Frame is one self-validating event record in a segment JSONL stream.
type Frame struct {
	FrameVersion  int             `json:"frame_version"`
	Family        string          `json:"family"`
	ID            string          `json:"id"`
	SchemaVersion string          `json:"schema_version,omitempty"`
	CreatedAt     string          `json:"created_at,omitempty"`
	SHA256        string          `json:"sha256"`
	Record        json.RawMessage `json:"record"`
}

// FrameInvalidError distinguishes a malformed frame from an I/O failure.
type FrameInvalidError struct {
	Reason string
	Cause  error
}

func (e *FrameInvalidError) Error() string {
	if e == nil {
		return "invalid event frame"
	}
	if e.Cause != nil {
		return fmt.Sprintf("invalid event frame: %s: %v", e.Reason, e.Cause)
	}
	return "invalid event frame: " + e.Reason
}

func (e *FrameInvalidError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type frameRecordHeader struct {
	ID            string `json:"id"`
	SchemaVersion string `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
}

func canonical(record []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, record); err != nil {
		return nil, fmt.Errorf("canonicalize event record: %w", err)
	}
	return compact.Bytes(), nil
}

func recordHash(record []byte) string {
	sum := sha256.Sum256(record)
	return hex.EncodeToString(sum[:])
}

func familyOf(record []byte, id string) string {
	var header frameRecordHeader
	if len(record) > 0 && json.Unmarshal(record, &header) == nil {
		switch header.SchemaVersion {
		case "compound.transition.v0.1":
			return "transition"
		case "compound.policy_decision.v0.1":
			return "policy"
		case "compound.hook_event.v0.1":
			return "hook"
		case "compound.action_intent.v0.1":
			return "action"
		case "compound.trace_envelope.v0.1":
			return "trace"
		case "compound.receipt.v0.1":
			return "receipt"
		}
	}
	switch {
	case strings.HasPrefix(id, "tr_"):
		return "transition"
	case strings.HasPrefix(id, "pol_"):
		return "policy"
	case strings.HasPrefix(id, "hook_"):
		return "hook"
	case strings.HasPrefix(id, "act_"), strings.HasPrefix(id, "act_live_"):
		return "action"
	case strings.HasPrefix(id, "trace_"):
		return "trace"
	case strings.HasPrefix(id, "rec_"):
		return "receipt"
	default:
		return "other"
	}
}

func buildFrame(id string, record []byte) (Frame, []byte, error) {
	if _, err := ReferenceForID(id); err != nil {
		return Frame{}, nil, err
	}
	compact, err := canonical(record)
	if err != nil {
		return Frame{}, nil, err
	}
	var header frameRecordHeader
	if err := json.Unmarshal(compact, &header); err != nil {
		return Frame{}, nil, &FrameInvalidError{Reason: "decode record header", Cause: err}
	}
	if header.ID != id {
		return Frame{}, nil, &FrameInvalidError{Reason: fmt.Sprintf("record id %q does not match put id %q", header.ID, id)}
	}
	frame := Frame{
		FrameVersion:  frameVersion,
		Family:        familyOf(compact, id),
		ID:            id,
		SchemaVersion: header.SchemaVersion,
		CreatedAt:     header.CreatedAt,
		SHA256:        recordHash(compact),
		Record:        append(json.RawMessage(nil), compact...),
	}
	return frame, compact, nil
}

func encodeFrame(frame Frame) ([]byte, error) {
	if err := validateFrame(frame); err != nil {
		return nil, err
	}
	line, err := json.Marshal(frame)
	if err != nil {
		return nil, &FrameInvalidError{Reason: "encode", Cause: err}
	}
	if bytes.ContainsRune(line, '\n') {
		return nil, &FrameInvalidError{Reason: "encoded frame contains an interior newline"}
	}
	return append(line, '\n'), nil
}

func decodeFrame(line []byte) (Frame, error) {
	if bytes.ContainsRune(line, '\n') {
		return Frame{}, &FrameInvalidError{Reason: "line contains an interior newline"}
	}
	var frame Frame
	if err := json.Unmarshal(line, &frame); err != nil {
		return Frame{}, &FrameInvalidError{Reason: "decode", Cause: err}
	}
	if err := validateFrame(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func validateFrame(frame Frame) error {
	if frame.FrameVersion != frameVersion {
		return &FrameInvalidError{Reason: fmt.Sprintf("frame_version=%d, want %d", frame.FrameVersion, frameVersion)}
	}
	if frame.ID == "" || frame.Family == "" {
		return &FrameInvalidError{Reason: "id and family are required"}
	}
	compact, err := canonical(frame.Record)
	if err != nil {
		return &FrameInvalidError{Reason: "canonicalize record", Cause: err}
	}
	if got := recordHash(compact); frame.SHA256 != got {
		return &FrameInvalidError{Reason: fmt.Sprintf("record checksum %q does not match %q", frame.SHA256, got)}
	}
	var header frameRecordHeader
	if err := json.Unmarshal(compact, &header); err != nil {
		return &FrameInvalidError{Reason: "decode record header", Cause: err}
	}
	if header.ID != frame.ID {
		return &FrameInvalidError{Reason: fmt.Sprintf("record id %q does not match frame id %q", header.ID, frame.ID)}
	}
	if header.SchemaVersion != frame.SchemaVersion {
		return &FrameInvalidError{Reason: fmt.Sprintf("record schema_version %q does not match frame %q", header.SchemaVersion, frame.SchemaVersion)}
	}
	if header.CreatedAt != frame.CreatedAt {
		return &FrameInvalidError{Reason: fmt.Sprintf("record created_at %q does not match frame %q", header.CreatedAt, frame.CreatedAt)}
	}
	if family := familyOf(compact, frame.ID); frame.Family != family {
		return &FrameInvalidError{Reason: fmt.Sprintf("record family %q does not match frame %q", family, frame.Family)}
	}
	return nil
}
