package watcher

func DeriveStatusFromProbe(probeErr error, result ProbeResult, consecutiveQueryErrors, threshold int) (status string, nextErrors int) {
	if threshold <= 0 {
		threshold = 5
	}

	if probeErr != nil {
		nextErrors = consecutiveQueryErrors + 1
		if nextErrors >= threshold {
			return "query_error", nextErrors
		}
		return "watching", nextErrors
	}

	nextErrors = 0
	switch result.Status {
	case "succeeded":
		return "succeeded", nextErrors
	case "failed":
		return "failed", nextErrors
	case "running":
		return "running", nextErrors
	default:
		return "watching", nextErrors
	}
}
