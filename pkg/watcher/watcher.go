package watcher

import pipestatus "porch/pkg/pipeline"

func DeriveStatusFromProbe(probeErr error, result ProbeResult, consecutiveQueryErrors, threshold int) (status pipestatus.Status, nextErrors int) {
	if threshold <= 0 {
		threshold = 5
	}

	if probeErr != nil {
		// Transient probe failures keep pipeline in WATCHING until threshold
		// is reached; this avoids false failures during short API outages.
		nextErrors = consecutiveQueryErrors + 1
		if nextErrors >= threshold {
			return pipestatus.StatusQueryErr, nextErrors
		}
		return pipestatus.StatusWatching, nextErrors
	}

	nextErrors = 0
	switch result.Status {
	case pipestatus.StatusSucceeded:
		return pipestatus.StatusSucceeded, nextErrors
	case pipestatus.StatusFailed:
		return pipestatus.StatusFailed, nextErrors
	case pipestatus.StatusRunning:
		return pipestatus.StatusRunning, nextErrors
	default:
		return pipestatus.StatusWatching, nextErrors
	}
}
