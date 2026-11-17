package corpus

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearchCLISelectedLaneFailuresGiveBackendHealthGuidance(t *testing.T) {
	for _, tc := range []struct {
		name        string
		indexed     bool
		expired     bool
		wantMessage string
	}{
		{name: "source query failure", wantMessage: "memory search: source query unavailable"},
		{name: "query deadline", expired: true, wantMessage: "memory search: source query unavailable"},
		{name: "backend unavailable", indexed: true, wantMessage: "memory search: backend unavailable; relevance could not be assessed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const diagnostic = "fixture-only private upstream diagnostic"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, diagnostic, http.StatusServiceUnavailable)
			}))
			defer server.Close()

			homePath := t.TempDir()
			lane, _ := seedCorpusLane(t, homePath, 1, 48, 0)
			lane.Embedder.BaseURL = server.URL + "/v1"
			if tc.indexed {
				embedder := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: lane.Embedder.Dims}
				if _, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true}); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if tc.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}

			const query = "fixture-only private query"
			var out, errout bytes.Buffer
			code := SearchCLI(ctx, homePath, []LaneConfig{lane}, []string{query, "--lane", lane.Lane, "--k", "1", "--json"}, &out, &errout)
			if code != 1 || out.Len() != 0 {
				t.Fatalf("failure returned code=%d stdout=%q", code, out.String())
			}
			message := errout.String()
			if !strings.Contains(message, tc.wantMessage) {
				t.Fatalf("failure category changed: %q", message)
			}
			if !strings.Contains(message, "compound corpus backend-health") {
				t.Fatalf("failure omits the existing diagnostic command: %q", message)
			}
			for _, private := range []string{diagnostic, homePath, query} {
				if strings.Contains(message, private) {
					t.Fatalf("failure exposed private detail %q: %q", private, message)
				}
			}
		})
	}
}
