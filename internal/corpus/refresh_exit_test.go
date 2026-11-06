package corpus

import "testing"

// A scheduled refresh is judged by Failed(), not by the headline status: a
// refused symlink or a source deleted after capture leaves a lane
// capture_incomplete and the report degraded, yet the lane captured and indexed
// everything it may, so the run succeeded. Only hard lane failures (or no lanes
// at all) make the run fail.
func TestRefreshReportFailedDistinguishesPartialCaptureFromHardFailure(t *testing.T) {
	cases := []struct {
		name   string
		report RefreshReport
		failed bool
	}{
		{"ok", RefreshReport{Status: "ok", Sources: []Coverage{{Status: "ok"}}}, false},
		{"backfilling", RefreshReport{Status: "backfilling", Sources: []Coverage{{Status: "backfilling"}}}, false},
		{"empty lane", RefreshReport{Status: "ok", Sources: []Coverage{{Status: "empty"}}}, false},
		{"refused symlink or deleted source", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "ok"}, {Status: "capture_incomplete", RefusedSymlinks: 5}, {Status: "capture_incomplete", MissingFiles: 31}}}, false},
		{"index failed", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "ok"}, {Status: "index_failed"}}}, true},
		{"embedder unavailable", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "embedder_unavailable"}}}, true},
		{"capture failed", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "capture_failed"}}}, true},
		{"invalid config", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "invalid_config"}}}, true},
		{"status write failed", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "status_write_failed"}}}, true},
		{"cancelled", RefreshReport{Status: "degraded", Sources: []Coverage{{Status: "cancelled"}}}, true},
		{"not configured", RefreshReport{Status: "not_configured"}, true},
	}
	for _, tc := range cases {
		if got := tc.report.Failed(); got != tc.failed {
			t.Errorf("%s: Failed()=%v want %v (%+v)", tc.name, got, tc.failed, tc.report)
		}
	}
}
