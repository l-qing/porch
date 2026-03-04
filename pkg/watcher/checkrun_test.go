package watcher

import (
	"testing"

	pipestatus "porch/pkg/pipeline"
)

func TestProbeFromCheckRun(t *testing.T) {
	tests := []struct {
		status     string
		conclusion string
		wantStatus pipestatus.Status
		wantConc   pipestatus.Conclusion
	}{
		{status: "completed", conclusion: "success", wantStatus: pipestatus.StatusSucceeded, wantConc: pipestatus.ConclusionSuccess},
		{status: "completed", conclusion: "failure", wantStatus: pipestatus.StatusFailed, wantConc: pipestatus.ConclusionFailure},
		{status: "completed", conclusion: "cancelled", wantStatus: pipestatus.StatusFailed, wantConc: pipestatus.ConclusionFailure},
		{status: "in_progress", conclusion: "", wantStatus: pipestatus.StatusRunning, wantConc: pipestatus.ConclusionUnknown},
		{status: "queued", conclusion: "", wantStatus: pipestatus.StatusRunning, wantConc: pipestatus.ConclusionUnknown},
	}

	for _, tc := range tests {
		got := ProbeFromCheckRun(tc.status, tc.conclusion)
		if got.Status != tc.wantStatus || got.Conclusion != tc.wantConc {
			t.Fatalf("status=%s conclusion=%s => got=%+v", tc.status, tc.conclusion, got)
		}
	}
}
