package watcher

import "testing"

func TestProbeFromCheckRun(t *testing.T) {
	tests := []struct {
		status     string
		conclusion string
		wantStatus string
		wantConc   string
	}{
		{status: "completed", conclusion: "success", wantStatus: "succeeded", wantConc: "success"},
		{status: "completed", conclusion: "failure", wantStatus: "failed", wantConc: "failure"},
		{status: "completed", conclusion: "cancelled", wantStatus: "failed", wantConc: "failure"},
		{status: "in_progress", conclusion: "", wantStatus: "running", wantConc: "unknown"},
		{status: "queued", conclusion: "", wantStatus: "running", wantConc: "unknown"},
	}

	for _, tc := range tests {
		got := ProbeFromCheckRun(tc.status, tc.conclusion)
		if got.Status != tc.wantStatus || got.Conclusion != tc.wantConc {
			t.Fatalf("status=%s conclusion=%s => got=%+v", tc.status, tc.conclusion, got)
		}
	}
}
