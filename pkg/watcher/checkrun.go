package watcher

import "strings"

func ProbeFromCheckRun(status, conclusion string) ProbeResult {
	s := strings.ToLower(strings.TrimSpace(status))
	c := strings.ToLower(strings.TrimSpace(conclusion))

	switch s {
	case "completed":
		switch c {
		case "success":
			return ProbeResult{Status: "succeeded", Reason: "gh_fallback", Conclusion: "success"}
		case "", "neutral", "cancelled", "timed_out", "action_required", "stale", "startup_failure", "failure":
			return ProbeResult{Status: "failed", Reason: "gh_fallback", Conclusion: "failure"}
		default:
			return ProbeResult{Status: "failed", Reason: "gh_fallback", Conclusion: "failure"}
		}
	case "in_progress", "queued", "pending", "requested", "waiting":
		return ProbeResult{Status: "running", Reason: "gh_fallback", Conclusion: "unknown"}
	default:
		return ProbeResult{Status: "running", Reason: "gh_fallback", Conclusion: "unknown"}
	}
}
