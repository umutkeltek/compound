package distiller

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMExtractionRequestPathResolvesRecordedRefsSafely(t *testing.T) {
	homePath := t.TempDir()
	path, err := LLMExtractionRequestPath(homePath, "distiller_request:llmreq_abc123")
	if err != nil {
		t.Fatalf("resolve recorded request ref: %v", err)
	}
	want := filepath.Join(homePath, "reviews", "distiller-requests", "llmreq_abc123.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	if _, err := LLMExtractionRequestPath(homePath, "distiller_request:../llmreq_escape"); err == nil || !strings.Contains(err.Error(), "invalid distiller request record id") {
		t.Fatalf("unsafe request ref error = %v", err)
	}

	plain := filepath.Join(homePath, "request.json")
	path, err = LLMExtractionRequestPath(homePath, plain)
	if err != nil {
		t.Fatalf("plain request path: %v", err)
	}
	if path != plain {
		t.Fatalf("plain path = %q, want %q", path, plain)
	}
}
