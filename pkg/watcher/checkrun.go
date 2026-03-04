package watcher

import (
	"strings"

	pipestatus "porch/pkg/pipeline"
)

func ProbeFromCheckRun(status, conclusion string) ProbeResult {
	s := strings.ToLower(strings.TrimSpace(status))
	c := strings.ToLower(strings.TrimSpace(conclusion))

	switch s {
	case "completed":
		switch c {
		case "success":
			return ProbeResult{Status: pipestatus.StatusSucceeded, Reason: "gh_fallback", Conclusion: pipestatus.ConclusionSuccess}
		case "", "neutral", "cancelled", "timed_out", "action_required", "stale", "startup_failure", "failure":
			return ProbeResult{Status: pipestatus.StatusFailed, Reason: "gh_fallback", Conclusion: pipestatus.ConclusionFailure}
		default:
			return ProbeResult{Status: pipestatus.StatusFailed, Reason: "gh_fallback", Conclusion: pipestatus.ConclusionFailure}
		}
	case "in_progress", "queued", "pending", "requested", "waiting":
		return ProbeResult{Status: pipestatus.StatusRunning, Reason: "gh_fallback", Conclusion: pipestatus.ConclusionUnknown}
	default:
		return ProbeResult{Status: pipestatus.StatusRunning, Reason: "gh_fallback", Conclusion: pipestatus.ConclusionUnknown}
	}
}
